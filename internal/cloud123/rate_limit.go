package cloud123

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type folderUploadRequest struct{}

type rateLimitError struct {
	code  string
	until time.Time
}

func (e rateLimitError) Error() string {
	return fmt.Sprintf("123 云盘暂时限制请求频率（%s），请在 %s 后继续", e.code, e.until.Local().Format("2006-01-02 15:04:05"))
}

func (c *Client) noteRateLimit(header, code string) rateLimitError {
	now := time.Now()
	until := now.Add(time.Minute)
	header = strings.TrimSpace(header)
	if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds > 0 {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if when, err := http.ParseTime(header); err == nil && when.After(now) {
		until = when
	}
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	if !c.rateLimit.until.After(until) {
		c.rateLimit = rateLimitError{code: code, until: until}
	}
	return c.rateLimit
}

func (c *Client) waitForRequest(ctx context.Context) error {
	batch, _ := ctx.Value(folderUploadRequest{}).(bool)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.requestMu.Lock()
		now := time.Now()
		if c.rateLimit.until.After(now) {
			err := c.rateLimit
			c.requestMu.Unlock()
			return err
		}
		if !batch || !c.nextBatchRequest.After(now) {
			if batch {
				c.nextBatchRequest = now.Add(c.batchInterval)
			}
			c.requestMu.Unlock()
			return nil
		}
		wait := c.nextBatchRequest.Sub(now)
		c.requestMu.Unlock()
		if err := waitForCloud(ctx, wait); err != nil {
			return err
		}
	}
}

func waitForCloud(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
