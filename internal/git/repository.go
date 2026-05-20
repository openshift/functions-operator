package git

import (
	"os"
	"path"
)

type Repository struct {
	CloneDir      string
	SubPath       string
	Commit        string
	Branch        string
	knownHostFile string
}

func (r *Repository) Path() string {
	return path.Join(r.CloneDir, r.SubPath)
}

func (r *Repository) Cleanup() error {
	if r.knownHostFile != "" {
		_ = os.Remove(r.knownHostFile)
	}
	return os.RemoveAll(r.CloneDir)
}
