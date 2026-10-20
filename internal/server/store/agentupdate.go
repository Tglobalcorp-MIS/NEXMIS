package store

import "context"

// AgentVersionCount is the number of agent-managed devices running one
// agent version, split by connection state.
type AgentVersionCount struct {
	Version string `json:"version"`
	Online  int    `json:"online"`
	Offline int    `json:"offline"`
}

// AgentVersionCounts groups devices with an agent by reported version;
// retired devices are left out.
func (s *Store) AgentVersionCounts(ctx context.Context) ([]AgentVersionCount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT agent_version,
		count(*) FILTER (WHERE online), count(*) FILTER (WHERE NOT online)
		FROM devices WHERE agent_id IS NOT NULL AND lifecycle_state <> 'retired'
		GROUP BY agent_version ORDER BY agent_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentVersionCount
	for rows.Next() {
		var c AgentVersionCount
		if err := rows.Scan(&c.Version, &c.Online, &c.Offline); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
