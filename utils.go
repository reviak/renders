package renders

import (
	"errors"
	"io/ioutil"
	"path/filepath"
)

func generateTemplateName(base, path string) string {
	return filepath.ToSlash(path[len(base)+1:])
}

func file_content(path string) (string, error) {
	// Read the file content of the template
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)

	if len(s) < 1 {
		return "", errors.New("render: template file is empty")
	}

	return s, nil
}

func inExtensions(ext string) bool {
	for _, e := range exts {
		if e == ext {
			return true
		}
	}
	return false
}