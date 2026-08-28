package storagekey

import "testing"

func TestBuildSplit(t *testing.T) {
	key := Build("uuid", "photo.jpg")
	if key != "uuid_photo.jpg" {
		t.Fatalf("Build = %q, want uuid_photo.jpg", key)
	}

	id, name := Split(key)
	if id != "uuid" || name != "photo.jpg" {
		t.Errorf("Split(%q) = (%q, %q), want (uuid, photo.jpg)", key, id, name)
	}

	// The name keeps everything after the first separator.
	id, name = Split("uuid_my_file_v2.txt")
	if id != "uuid" || name != "my_file_v2.txt" {
		t.Errorf("Split multi-underscore = (%q, %q), want (uuid, my_file_v2.txt)", id, name)
	}

	// No separator: the whole key is the name, with no id.
	id, name = Split("nofile")
	if id != "" || name != "nofile" {
		t.Errorf("Split no-separator = (%q, %q), want (\"\", nofile)", id, name)
	}
}
