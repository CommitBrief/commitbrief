package config

import "fmt"

func Migrate(c *Config) error {
	if c.Version == 0 {
		c.Version = CurrentSchemaVersion
	}
	if c.Version > CurrentSchemaVersion {
		return fmt.Errorf("config: schema version %d is from a newer CommitBrief release (this binary supports up to v%d); upgrade the binary or downgrade the config", c.Version, CurrentSchemaVersion)
	}
	return nil
}
