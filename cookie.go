package wireguard

import (
	"crypto/rand"
	"io"
	"time"
)

type cookieChecker struct {
	secret       [32]byte
	previous     [32]byte
	havePrevious bool
	have         bool
	born         time.Time
}

func (c *cookieChecker) refresh(r io.Reader, now time.Time, interval time.Duration) error {
	if !c.have {
		if _, err := io.ReadFull(r, c.secret[:]); err != nil {
			return err
		}
		c.born = now
		c.have = true
		return nil
	}
	delta := now.Sub(c.born)
	if delta < interval {
		return nil
	}
	periods := delta / interval
	if periods == 1 {
		c.previous = c.secret
		c.havePrevious = true
	} else {
		c.previous = [32]byte{}
		c.havePrevious = false
	}
	if _, err := io.ReadFull(r, c.secret[:]); err != nil {
		return err
	}
	c.born = now
	return nil
}

func (c *cookieChecker) cookieFor(suite Suite, source [18]byte) [16]byte {
	return suite.MAC(c.secret[:], source[:])
}

func (c *cookieChecker) validMAC2(suite Suite, source [18]byte, pkt []byte, mac2Offset int, now time.Time, interval time.Duration, r io.Reader) (bool, error) {
	if err := c.refresh(r, now, interval); err != nil {
		return false, err
	}
	current := c.cookieFor(suite, source)
	if checkMAC2(suite, pkt, mac2Offset, current) {
		return true, nil
	}
	if c.havePrevious {
		previous := suite.MAC(c.previous[:], source[:])
		if checkMAC2(suite, pkt, mac2Offset, previous) {
			return true, nil
		}
	}
	return false, nil
}

type cookieStore struct {
	cookie [16]byte
	have   bool
}

func (c *cookieStore) set(cookie [16]byte) {
	c.cookie = cookie
	c.have = true
}

func (c *cookieStore) get() ([16]byte, bool) {
	return c.cookie, c.have
}

var defaultRand io.Reader = rand.Reader
