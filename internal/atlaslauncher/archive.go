package atlaslauncher

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func extract(ctx context.Context, archive, name, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	if strings.HasSuffix(name, ".zip") {
		return extractZip(ctx, archive, root)
	}
	return extractTarGz(ctx, archive, root)
}

func extractTarGz(ctx context.Context, archive string, root *os.Root) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeDir:
		default:
			return fmt.Errorf("unsupported archive entry %q (tar type %d)", header.Name, header.Typeflag)
		}
		if err := extractEntry(root, header.Name, header.FileInfo().Mode(), reader); err != nil {
			return err
		}
	}
}

func extractZip(ctx context.Context, archive string, root *os.Root) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := file.Open()
		if err != nil {
			return err
		}
		err = extractEntry(root, file.Name, file.Mode(), body)
		closeErr := body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractEntry(root *os.Root, name string, mode fs.FileMode, body io.Reader) error {
	rel := strings.TrimPrefix(name, "./")
	if mode.IsDir() {
		rel = strings.TrimSuffix(rel, "/")
		if rel == "." || name == "./" {
			return nil
		}
	}
	local, err := filepath.Localize(rel)
	if err != nil || rel == "." || strings.ContainsAny(rel, "\\:") || rel == markerName || rel == pendingMarker {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	if mode.IsDir() {
		return root.MkdirAll(local, 0o755)
	}
	// Atlas archives contain regular files and directories. Reject links
	// and special entries without creating anything at their paths.
	if !mode.IsRegular() {
		return fmt.Errorf("unsupported archive entry %q (%s)", name, mode)
	}
	if err := root.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	file, err := root.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, body)
	if err == nil {
		err = file.Chmod(mode.Perm())
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
