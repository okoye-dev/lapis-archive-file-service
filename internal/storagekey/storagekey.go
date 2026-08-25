// Package storagekey builds and parses the "<fileID>_<fileName>" object keys.
package storagekey

import "strings"

// Build joins a file id and name into a storage key.
func Build(fileID, fileName string) string {
	return fileID + "_" + fileName
}

// Split separates a storage key into its file id and file name. A key with no
// separator has no id, so the whole key is returned as the name.
func Split(key string) (fileID, fileName string) {
	id, name, found := strings.Cut(key, "_")
	if !found {
		return "", key
	}
	return id, name
}
