package catalog

type CleanupAttempt struct {
	Attempt int    `json:"attempt"`
	Status  string `json:"status"`
	At      string `json:"at"`
	Detail  string `json:"detail"`
	Reason  string `json:"reason"`
}

func (c *Catalog) CleanupAttempts(job, instance string) ([]CleanupAttempt, error) {
	rows, err := c.db.Query("SELECT attempt,status,at,detail,reason FROM cleanup_attempts WHERE job=? AND instance=? ORDER BY attempt DESC LIMIT 100", job, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CleanupAttempt{}
	for rows.Next() {
		var row CleanupAttempt
		if err = rows.Scan(&row.Attempt, &row.Status, &row.At, &row.Detail, &row.Reason); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
