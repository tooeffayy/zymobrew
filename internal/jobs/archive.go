package jobs

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Export format identifiers as exposed in the API and stored in user_exports.format.
const (
	ExportFormatZip   = "zip"
	ExportFormatTarGz = "tar.gz"
	ExportFormatZstd  = "zstd"
)

// archiveWriter is a tiny abstraction over the supported export containers.
// Entries are added whole — JSON payloads are small, and tar requires the
// entry size up front, so a streaming "open writer" API would force a
// wrapper-side buffer anyway.
type archiveWriter interface {
	AddEntry(name string, body []byte) error
	Close() error
}

// FormatExtension maps an export format to the file extension used in
// storage keys and download filenames.
func FormatExtension(format string) string {
	switch format {
	case ExportFormatTarGz:
		return "tar.gz"
	case ExportFormatZstd:
		return "tar.zst"
	default:
		return "zip"
	}
}

// FormatContentType maps an export format to the HTTP Content-Type sent on
// direct downloads from the local backend.
func FormatContentType(format string) string {
	switch format {
	case ExportFormatTarGz:
		return "application/gzip"
	case ExportFormatZstd:
		return "application/zstd"
	default:
		return "application/zip"
	}
}

// IsValidExportFormat reports whether the format is one we accept on the API.
func IsValidExportFormat(format string) bool {
	switch format {
	case ExportFormatZip, ExportFormatTarGz, ExportFormatZstd:
		return true
	}
	return false
}

func newArchiveWriter(format string, out io.Writer) (archiveWriter, error) {
	switch format {
	case ExportFormatZip, "":
		return &zipArchive{w: zip.NewWriter(out)}, nil
	case ExportFormatTarGz:
		gw := gzip.NewWriter(out)
		return &tarArchive{tw: tar.NewWriter(gw), cw: gw}, nil
	case ExportFormatZstd:
		zw, err := zstd.NewWriter(out)
		if err != nil {
			return nil, err
		}
		return &tarArchive{tw: tar.NewWriter(zw), cw: zw}, nil
	default:
		return nil, errors.New("unsupported export format: " + format)
	}
}

type zipArchive struct{ w *zip.Writer }

func (z *zipArchive) AddEntry(name string, body []byte) error {
	f, err := z.w.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	return err
}

func (z *zipArchive) Close() error { return z.w.Close() }

// tarArchive wraps a tar writer atop a compression writer. Close order matters:
// the tar trailer must be flushed before the compressor's footer, otherwise
// the archive is silently truncated.
type tarArchive struct {
	tw *tar.Writer
	cw io.WriteCloser
}

func (t *tarArchive) AddEntry(name string, body []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(body)),
		ModTime: time.Now().UTC(),
	}
	if err := t.tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := t.tw.Write(body)
	return err
}

func (t *tarArchive) Close() error {
	if err := t.tw.Close(); err != nil {
		_ = t.cw.Close()
		return err
	}
	return t.cw.Close()
}
