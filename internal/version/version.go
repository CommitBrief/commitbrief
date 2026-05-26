package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func Info() string {
	return fmt.Sprintf("commitbrief %s (commit %s, built %s)", Version, Commit, Date)
}
