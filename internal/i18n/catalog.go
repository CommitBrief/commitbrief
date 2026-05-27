// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

import "fmt"

type Catalog struct {
	code     string
	messages map[string]string
}

func (c *Catalog) Code() string { return c.code }

func (c *Catalog) Has(key string) bool {
	_, ok := c.messages[key]
	return ok
}

func (c *Catalog) T(key string, args ...any) string {
	msg, ok := c.messages[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
