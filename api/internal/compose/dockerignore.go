package compose

import (
	"os"
	"path/filepath"
)

const missingDockerignoreWarning = "No .dockerignore found — build context may include unwanted files (node_modules/, .git/, etc.). Consider adding one for faster builds."

// WarnIfMissingDockerignore returns a non-empty warning string if the repo
// does not contain a .dockerignore file. The pipeline writes the warning
// into deployment_logs so it appears in the UI build log.
func WarnIfMissingDockerignore(repoDir string) string {
	if _, err := os.Stat(filepath.Join(repoDir, ".dockerignore")); err == nil {
		return ""
	}
	return missingDockerignoreWarning
}
