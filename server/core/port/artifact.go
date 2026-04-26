package port

// DocumentArtifactStore persists and retrieves document artifact files.
// Implemented by store/filesystem.
type DocumentArtifactStore interface {
	Save(vaultPath, artifactID, filename string, data []byte) error
	Read(vaultPath, artifactID, filename string) ([]byte, error)
	// SaveAt writes data to a vault-relative path. Used for run-output
	// artifacts that live under organized `runs/<job>/<run>/...` paths.
	SaveAt(vaultPath, relPath string, data []byte) error
	// ReadAt reads from a vault-relative path. Used to serve and to load
	// run-output artifacts back into stage inputs.
	ReadAt(vaultPath, relPath string) ([]byte, error)
}
