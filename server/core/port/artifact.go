package port

// DocumentArtifactStore persists and retrieves document artifact files.
// Implemented by store/filesystem.
type DocumentArtifactStore interface {
	SaveArtifact(vaultPath, artifactID, filename string, data []byte) error
	ReadArtifact(vaultPath, artifactID, filename string) ([]byte, error)
}
