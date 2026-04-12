package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReadPlanFixture reads a plan fixture file, trying testDir first then suiteDir.
func ReadPlanFixture(testDir, suiteDir, filename string) ([]byte, error) {
	p := filepath.Join(testDir, filename)
	b, err := os.ReadFile(p)
	if err != nil {
		p = filepath.Join(suiteDir, filename)
		b, err = os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read fixture %s: %w", filename, err)
		}
	}
	return b, nil
}
