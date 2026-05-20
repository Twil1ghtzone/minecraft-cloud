package sftp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	pkgsftp "github.com/pkg/sftp"
)

// chrootHandler implements all four pkgsftp.Handlers interfaces and resolves
// every path relative to `root`. Any traversal outside root is rejected.
type chrootHandler struct {
	root string
}

func (c *chrootHandler) resolve(reqPath string) (string, error) {
	clean := filepath.Clean("/" + reqPath)
	full := filepath.Join(c.root, clean)
	rel, err := filepath.Rel(c.root, full)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", errors.New("path escapes chroot")
	}
	return full, nil
}

func (c *chrootHandler) Fileread(r *pkgsftp.Request) (io.ReaderAt, error) {
	p, err := c.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (c *chrootHandler) Filewrite(r *pkgsftp.Request) (io.WriterAt, error) {
	p, err := c.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}

func (c *chrootHandler) Filecmd(r *pkgsftp.Request) error {
	src, err := c.resolve(r.Filepath)
	if err != nil {
		return err
	}
	switch r.Method {
	case "Setstat":
		return nil
	case "Rename":
		dst, err := c.resolve(r.Target)
		if err != nil {
			return err
		}
		return os.Rename(src, dst)
	case "Rmdir":
		return os.Remove(src)
	case "Mkdir":
		return os.MkdirAll(src, 0o755)
	case "Remove":
		return os.Remove(src)
	}
	return errors.New("unsupported sftp command: " + r.Method)
}

func (c *chrootHandler) Filelist(r *pkgsftp.Request) (pkgsftp.ListerAt, error) {
	p, err := c.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			infos = append(infos, info)
		}
		return listerAt(infos), nil
	case "Stat":
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		return listerAt([]os.FileInfo{fi}), nil
	}
	return nil, errors.New("unsupported list method: " + r.Method)
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}
