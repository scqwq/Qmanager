package cloud123

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const uploadAuthorizationBatch = 10

func (c *Client) checkMultipartSession(ctx context.Context, upload uploadInit, total int64) error {
	reply, err := c.post(ctx, "/b/api/file/s3_list_upload_parts", map[string]any{
		"bucket": upload.Bucket, "key": upload.Key, "uploadId": upload.UploadID, "StorageNode": upload.StorageNode,
	})
	if err != nil {
		return fmt.Errorf("无法查询云端分片，%w", err)
	}
	var result struct {
		Parts json.RawMessage `json:"Parts"`
	}
	var existing []struct {
		Number json.Number `json:"PartNumber"`
	}
	if json.Unmarshal(reply.Data, &result) != nil || len(result.Parts) == 0 ||
		json.Unmarshal(result.Parts, &existing) != nil || int64(len(existing)) > total {
		return fmt.Errorf("云端分片清单无法核实")
	}
	seen := make(map[int64]bool, len(existing))
	for _, part := range existing {
		number, err := part.Number.Int64()
		if err != nil || number < 1 || number > total || seen[number] {
			return fmt.Errorf("云端分片编号与本次文件不符")
		}
		seen[number] = true
	}
	// The current session may contain parts from an earlier failed attempt.
	// Send all parts from the checked local file before completing this session.
	return nil
}

func (c *Client) authorizeParts(ctx context.Context, upload uploadInit, first, last, total int64) ([]*url.URL, error) {
	endpoint := "/b/api/file/s3_repare_upload_parts_batch"
	if total == 1 {
		endpoint = "/b/api/file/s3_upload_object/auth"
	}
	var returned string
	var missing int64
	for attempt := 0; attempt < 2; attempt++ {
		auth, err := c.post(ctx, endpoint, map[string]any{
			"bucket": upload.Bucket, "key": upload.Key, "uploadId": upload.UploadID,
			"StorageNode": upload.StorageNode, "partNumberStart": first, "partNumberEnd": last + 1,
		})
		if err != nil {
			return nil, fmt.Errorf("从第 %d/%d 片起申请上传地址失败，%w", first, total, err)
		}
		var data struct {
			URLs map[string]string `json:"presignedUrls"`
		}
		if json.Unmarshal(auth.Data, &data) != nil {
			return nil, fmt.Errorf("从第 %d/%d 片起的授权响应格式无法识别", first, total)
		}
		returned = returnedParts(data.URLs)
		addresses := make([]*url.URL, 0, last-first+1)
		missing = 0
		for part := first; part <= last; part++ {
			signed := strings.TrimSpace(data.URLs[strconv.FormatInt(part, 10)])
			if signed == "" {
				missing = part
				break
			}
			address, err := trustedObjectURL(signed)
			if err != nil {
				return nil, fmt.Errorf("第 %d/%d 片，%w（返回分片编号：%s）", part, total, err, returned)
			}
			addresses = append(addresses, address)
		}
		if missing == 0 {
			return addresses, nil
		}
		if attempt == 0 {
			if err := waitForCloud(ctx, time.Second); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("云盘授权响应缺少第 %d/%d 片的上传地址（返回分片编号：%s）", missing, total, returned)
}

func returnedParts(urls map[string]string) string {
	numbers := make([]int, 0, len(urls))
	for key := range urls {
		if n, err := strconv.Atoi(key); err == nil && n > 0 && n <= 10000 {
			numbers = append(numbers, n)
		}
	}
	if len(numbers) == 0 {
		return "无"
	}
	sort.Ints(numbers)
	labels := make([]string, 0, 9)
	for _, n := range numbers[:min(8, len(numbers))] {
		labels = append(labels, strconv.Itoa(n))
	}
	if len(numbers) > 8 {
		labels = append(labels, "…")
	}
	return strings.Join(labels, "、")
}
