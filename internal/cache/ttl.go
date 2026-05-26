package cache

import "time"

const DefaultTTL = 7 * 24 * time.Hour

func (e Entry) Expired() bool {
	return e.ExpiredAt(time.Now())
}

func (e Entry) ExpiredAt(now time.Time) bool {
	if e.TTL <= 0 {
		return false
	}
	return now.Sub(e.CreatedAt) > time.Duration(e.TTL)*time.Second
}
