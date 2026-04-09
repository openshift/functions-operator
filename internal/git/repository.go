package git

import (
	"os"
	"path"
)

type Repository struct {
	CloneDir string
	SubPath  string
	Commit   string
	Branch   string
}

func (r *Repository) Path() string {
	return path.Join(r.CloneDir, r.SubPath)
}

func (r *Repository) Cleanup() error {
	return os.RemoveAll(r.CloneDir)
}
