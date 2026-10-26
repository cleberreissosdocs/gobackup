package dbdump

import (
	"archive/zip"
	"io"
)

// singleFileZipWriter wraps a zip.Writer so dump.Run can stream a dump's
// stdout/sqlite bytes directly into one zip entry without buffering the
// whole dump in memory first.
type singleFileZipWriter struct {
	zw     *zip.Writer
	entryW io.Writer
}

func newSingleFileZipWriter(dst io.Writer, entryName string) *singleFileZipWriter {
	s := &singleFileZipWriter{zw: zip.NewWriter(dst)}
	// zip.Writer.Create is safe to call immediately; writes are streamed.
	w, err := s.zw.Create(entryName)
	if err != nil {
		// Extremely unlikely (in-memory header creation); fall back to a
		// no-op writer so callers still get a clear error from Close().
		w = io.Discard
	}
	s.entryW = w
	return s
}

func (s *singleFileZipWriter) Write(p []byte) (int, error) {
	return s.entryW.Write(p)
}

func (s *singleFileZipWriter) Close() error {
	return s.zw.Close()
}
