package core

import (
	"regexp"
	"strings"
)

// Exclusions let a file-tree backup job skip paths that should never be
// copied — caches, temp dirs, huge scratch files, a subdirectory that is
// already backed up by another job, and so on.
//
// The pattern language is rsync's (that is what the Linux agent passes
// straight through to `rsync --exclude`), and the subset implemented here
// in Go — for the Windows agent, which mirrors files itself instead of
// shelling out to rsync — is deliberately the same so one pattern written
// in the UI behaves identically on both platforms:
//
//	*.tmp          any file ending in .tmp, at any depth
//	cache          any file OR directory named "cache", at any depth
//	cache/         only a DIRECTORY named "cache" (and everything inside it)
//	/cache         only "cache" at the top level of the source, not deeper
//	logs/*.gz      .gz files directly inside any "logs" directory
//	**/node_modules  "node_modules" at any depth (explicit form of the above)
//
// Rules, matching rsync: a pattern with no "/" is matched against each
// path component's NAME at any depth; a pattern containing "/" is matched
// against the path relative to the backup source, and if it does not start
// with "/" it may also match at any depth. "*" never crosses a "/", "**"
// does, "?" is a single non-"/" character. Excluding a directory excludes
// its entire subtree.

// ParseExcludePatterns turns the raw contents of the job form's exclusions
// textarea into a clean pattern list: one per line, trimmed, with blank
// lines and "#" comments dropped. Accepts both "\n" and "\r\n" (a browser
// textarea submits CRLF).
func ParseExcludePatterns(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// JoinExcludePatterns is the inverse of ParseExcludePatterns: the storage
// form (one pattern per line, a single TEXT column) and what the "Editar"
// form prefills its textarea with.
func JoinExcludePatterns(patterns []string) string {
	return strings.Join(patterns, "\n")
}

// ExcludeMatcher decides whether a path relative to the backup source is
// excluded. Used by the Windows agent's own mirror walk; the Linux agent
// hands the raw patterns to rsync instead, which applies these same rules
// natively.
type ExcludeMatcher struct {
	// anchored patterns must match from the source root; floating ones
	// may match at any depth. dirOnly patterns only match directories.
	rules []excludeRule
}

type excludeRule struct {
	re       *regexp.Regexp
	anchored bool
	dirOnly  bool
	baseOnly bool // pattern had no "/": match against the component name
}

// NewExcludeMatcher compiles patterns once, up front. Invalid patterns are
// skipped rather than failing the whole backup — a typo in one exclusion
// must never be the reason a backup doesn't run.
func NewExcludeMatcher(patterns []string) *ExcludeMatcher {
	m := &ExcludeMatcher{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		r := excludeRule{}
		if strings.HasSuffix(p, "/") {
			r.dirOnly = true
			p = strings.TrimSuffix(p, "/")
		}
		if strings.HasPrefix(p, "/") {
			r.anchored = true
			p = strings.TrimPrefix(p, "/")
		}
		if p == "" {
			continue
		}
		// A leading "**/" means "at any depth" — including the top level.
		// Taken literally the "**" would demand at least one "/" before
		// the name, so "**/node_modules" would skip a node_modules
		// sitting directly in the source root, which is never what
		// someone writing that pattern means (gitignore reads it the
		// same way).
		p = strings.TrimPrefix(p, "**/")
		if p == "" {
			continue
		}
		r.baseOnly = !strings.Contains(p, "/")
		re, err := compileGlob(p)
		if err != nil {
			continue
		}
		r.re = re
		m.rules = append(m.rules, r)
	}
	return m
}

// Empty reports whether there is nothing to exclude, so callers can skip
// the per-path work entirely on the common no-exclusions path.
func (m *ExcludeMatcher) Empty() bool { return m == nil || len(m.rules) == 0 }

// Match reports whether this exact path is excluded by some pattern.
// rel is relative to the backup source, using "/" or the OS separator.
func (m *ExcludeMatcher) Match(rel string, isDir bool) bool {
	if m.Empty() {
		return false
	}
	rel = normalizeRel(rel)
	if rel == "" || rel == "." {
		return false
	}
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		base = rel[i+1:]
	}
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.baseOnly {
			if !r.anchored && r.re.MatchString(base) {
				return true
			}
			if r.anchored && r.re.MatchString(rel) {
				return true
			}
			continue
		}
		if r.re.MatchString(rel) {
			return true
		}
		// A non-anchored pattern containing "/" may also match deeper in
		// the tree ("logs/*.gz" matching "app/logs/x.gz"), same as rsync.
		if !r.anchored {
			for i := 0; i < len(rel); i++ {
				if rel[i] == '/' && r.re.MatchString(rel[i+1:]) {
					return true
				}
			}
		}
	}
	return false
}

// Excluded reports whether this path is excluded either directly or
// because one of its parent directories is — excluding a directory
// excludes its whole subtree. Walkers that prune excluded directories can
// use the cheaper Match instead.
func (m *ExcludeMatcher) Excluded(rel string, isDir bool) bool {
	if m.Empty() {
		return false
	}
	rel = normalizeRel(rel)
	if m.Match(rel, isDir) {
		return true
	}
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && m.Match(rel[:i], true) {
			return true
		}
	}
	return false
}

func normalizeRel(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return strings.Trim(rel, "/")
}

// compileGlob translates one rsync-style glob into an anchored regexp:
// "**" crosses "/" separators, "*" and "?" do not, "[...]" is a character
// class, everything else is literal.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`\A`)
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(`.*`)
				i++
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				b.WriteString(regexp.QuoteMeta(string(c)))
				continue
			}
			class := pattern[i : i+end+1]
			b.WriteString(class)
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}
