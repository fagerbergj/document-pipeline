package filesystem

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Store implements port.DocumentArtifactStore using the local filesystem.
// Artifacts are saved at <vaultPath>/artifacts/<artifactID>/<filename>.
type Store struct{}

var _ port.DocumentArtifactStore = (*Store)(nil)

func New() *Store { return &Store{} }

func (s *Store) Save(vaultPath, artifactID, filename string, data []byte) error {
	dir := filepath.Join(vaultPath, "artifacts", artifactID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("filesystem: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("filesystem: write %s: %w", path, err)
	}
	return nil
}

func (s *Store) Read(vaultPath, artifactID, filename string) ([]byte, error) {
	path := filepath.Join(vaultPath, "artifacts", artifactID, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("filesystem: read %s: %w", path, err)
	}
	return data, nil
}
