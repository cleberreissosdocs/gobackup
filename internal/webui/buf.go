package webui

import "strings"

// writerBuf is a tiny alias so handlers.go doesn't need to import "strings"
// just for this one helper type.
type writerBuf struct{ strings.Builder }
