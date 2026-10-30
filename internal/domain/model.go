package domain

import (
	"fmt"
	"time"
	_ "time/tzdata"
)

const ExporterVersion = "0.1.0-dev"

type Selection struct {
	Conversation string    `json:"conversation"`
	Timezone     string    `json:"timezone"`
	Start        time.Time `json:"start_utc"`
	End          time.Time `json:"end_exclusive_utc"`
}

func Dates(conversation, start, end, zone string) (Selection, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Selection{}, err
	}
	a, err := time.ParseInLocation("2006-01-02", start, loc)
	if err != nil {
		return Selection{}, err
	}
	b, err := time.ParseInLocation("2006-01-02", end, loc)
	if err != nil {
		return Selection{}, err
	}
	if conversation == "" || b.Before(a) {
		return Selection{}, fmt.Errorf("invalid selection")
	}
	return Selection{conversation, zone, a.UTC(), b.AddDate(0, 0, 1).UTC()}, nil
}

type Message struct {
	Key       string `json:"key"`
	Sender    string `json:"sender"`
	Timestamp string `json:"timestamp"`
	Sequence  int64  `json:"sequence"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Raw       string `json:"raw"`
}
type Manifest struct {
	Format            string    `json:"format"`
	Major             int       `json:"major"`
	Minor             int       `json:"minor"`
	ArchiveID         string    `json:"archive_id"`
	Created           string    `json:"created_utc"`
	Exporter          string    `json:"exporter_version"`
	Parser            string    `json:"parser_version"`
	SourceType        string    `json:"source_type"`
	SourceVersion     string    `json:"source_qq_version"`
	SchemaFingerprint string    `json:"source_schema_fingerprint"`
	Account           string    `json:"account_id"`
	ConversationID    string    `json:"conversation_id"`
	ConversationType  string    `json:"conversation_type"`
	ConversationName  string    `json:"conversation_name"`
	Selection         Selection `json:"selection"`
	Messages          int64     `json:"message_count"`
	Parsed            int64     `json:"parsed_count"`
	Unsupported       int64     `json:"unsupported_count"`
	MediaReferences   int64     `json:"media_reference_count"`
	MediaObjects      int64     `json:"unique_object_count"`
	Missing           int64     `json:"missing_count"`
	Consistency       string    `json:"consistency"`
	Integrity         string    `json:"content_status"`
	Algorithm         string    `json:"checksum_algorithm"`
}
