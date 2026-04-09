package port

// DocumentArtifactStore persists and retrieves document artifact files.
// Implemented by store/filesystem.
type DocumentArtifactStore interface {
	Save(vaultPath, artifactID, filename string, data []byte) error
	Read(vaultPath, artifactID, filename string) ([]byte, error)
}
