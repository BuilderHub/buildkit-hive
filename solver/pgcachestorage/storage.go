package pgcachestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	s3remotecache "github.com/moby/buildkit/cache/remotecache/s3"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/bklog"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

const schemaVersion = 1

// Store implements solver.CacheKeyStorage using Postgres for global shared metadata.
type Store struct {
	pool  *pgxpool.Pool
	group string
}

var _ solver.CacheKeyStorage = (*Store)(nil)

// NewStore creates a new Postgres-backed cache store.
// group isolates cache metadata when multiple tenants share the same database (default: global).
func NewStore(ctx context.Context, dsn string, group string) (*Store, error) {
	group, err := s3remotecache.ValidateCacheGroup(group)
	if err != nil {
		return nil, err
	}
	var pool *pgxpool.Pool
	for attempt := 0; attempt < 5; attempt++ {
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			bklog.L.WithError(err).Warnf("postgres connect attempt %d failed", attempt+1)
			time.Sleep(time.Second << attempt)
			continue
		}
		if err = pool.Ping(ctx); err != nil {
			pool.Close()
			bklog.L.WithError(err).Warnf("postgres ping attempt %d failed", attempt+1)
			time.Sleep(time.Second << attempt)
			continue
		}
		break
	}
	if pool == nil {
		return nil, errors.Wrap(err, "failed to connect to postgres after retries")
	}

	s := &Store{pool: pool, group: group}
	if err := s.initSchema(ctx); err != nil {
		pool.Close()
		return nil, errors.Wrap(err, "failed to initialize postgres cache schema")
	}
	return s, nil
}

func (s *Store) initSchema(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY
		);
		CREATE TABLE IF NOT EXISTS cache_links (
			id TEXT PRIMARY KEY
		);
		CREATE TABLE IF NOT EXISTS cache_results (
			id TEXT NOT NULL,
			result_id TEXT NOT NULL,
			data JSONB NOT NULL,
			PRIMARY KEY (id, result_id)
		);
		CREATE TABLE IF NOT EXISTS cache_by_result (
			result_id TEXT NOT NULL,
			id TEXT NOT NULL,
			PRIMARY KEY (result_id, id)
		);
		CREATE TABLE IF NOT EXISTS cache_backlinks (
			target_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			PRIMARY KEY (target_id, source_id)
		);
		CREATE TABLE IF NOT EXISTS cache_link_forward (
			source_id TEXT NOT NULL,
			link_key TEXT NOT NULL,
			target_id TEXT NOT NULL,
			PRIMARY KEY (source_id, link_key)
		);
		CREATE INDEX IF NOT EXISTS idx_results_id ON cache_results(id);
		CREATE INDEX IF NOT EXISTS idx_by_result_result_id ON cache_by_result(result_id);
		CREATE INDEX IF NOT EXISTS idx_by_result_id ON cache_by_result(id);
		CREATE INDEX IF NOT EXISTS idx_link_forward_source ON cache_link_forward(source_id, link_key text_pattern_ops);
	`)
	if err != nil {
		return err
	}

	var version int
	err = tx.QueryRow(ctx, `SELECT version FROM schema_migrations LIMIT 1`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, schemaVersion); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

func (s *Store) scopeID(id string) string {
	return s.group + "::" + id
}

func (s *Store) unscopeID(scoped string) string {
	prefix := s.group + "::"
	if strings.HasPrefix(scoped, prefix) {
		return strings.TrimPrefix(scoped, prefix)
	}
	return scoped
}

func (s *Store) Exists(id string) bool {
	var exists bool
	err := s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM cache_links WHERE id = $1)`, s.scopeID(id)).Scan(&exists)
	return err == nil && exists
}

func (s *Store) Walk(fn func(id string) error) error {
	rows, err := s.pool.Query(context.Background(), `SELECT id FROM cache_links`)
	if err != nil {
		return err
	}
	defer rows.Close()

	prefix := s.group + "::"
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if err := fn(s.unscopeID(id)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) WalkResults(id string, fn func(solver.CacheResult) error) error {
	rows, err := s.pool.Query(context.Background(), `SELECT data FROM cache_results WHERE id = $1`, s.scopeID(id))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var res solver.CacheResult
		if err := json.Unmarshal(data, &res); err != nil {
			return err
		}
		if err := fn(res); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) Load(id, resultID string) (solver.CacheResult, error) {
	var data []byte
	err := s.pool.QueryRow(context.Background(), `SELECT data FROM cache_results WHERE id = $1 AND result_id = $2`, s.scopeID(id), resultID).Scan(&data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return solver.CacheResult{}, errors.WithStack(solver.ErrNotFound)
		}
		return solver.CacheResult{}, err
	}
	var res solver.CacheResult
	if err := json.Unmarshal(data, &res); err != nil {
		return solver.CacheResult{}, err
	}
	return res, nil
}

// GetResultByID returns any stored cache result payload for the given result ID.
func (s *Store) GetResultByID(resultID string) (solver.CacheResult, error) {
	var data []byte
	err := s.pool.QueryRow(context.Background(), `SELECT data FROM cache_results WHERE result_id = $1 LIMIT 1`, resultID).Scan(&data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return solver.CacheResult{}, errors.WithStack(solver.ErrNotFound)
		}
		return solver.CacheResult{}, err
	}
	var res solver.CacheResult
	if err := json.Unmarshal(data, &res); err != nil {
		return solver.CacheResult{}, err
	}
	return res, nil
}

func (s *Store) AddResult(id string, res solver.CacheResult) error {
	tx, err := s.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())

	data, err := json.Marshal(res)
	if err != nil {
		return err
	}

	scopedID := s.scopeID(id)
	_, err = tx.Exec(context.Background(), `INSERT INTO cache_links (id) VALUES ($1) ON CONFLICT DO NOTHING`, scopedID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(context.Background(), `
		INSERT INTO cache_results (id, result_id, data) VALUES ($1, $2, $3)
		ON CONFLICT (id, result_id) DO UPDATE SET data = EXCLUDED.data
	`, scopedID, res.ID, data)
	if err != nil {
		return err
	}

	_, err = tx.Exec(context.Background(), `INSERT INTO cache_by_result (result_id, id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, res.ID, scopedID)
	if err != nil {
		return err
	}

	return tx.Commit(context.Background())
}

func (s *Store) WalkIDsByResult(resultID string, fn func(string) error) error {
	rows, err := s.pool.Query(context.Background(), `SELECT id FROM cache_by_result WHERE result_id = $1`, resultID)
	if err != nil {
		return err
	}
	defer rows.Close()

	prefix := s.group + "::"
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if err := fn(s.unscopeID(id)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) Release(resultID string) error {
	tx, err := s.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())

	rows, err := tx.Query(context.Background(), `SELECT id FROM cache_by_result WHERE result_id = $1`, resultID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return errors.WithStack(solver.ErrNotFound)
	}

	for _, id := range ids {
		if err := s.releaseHelper(tx, id, resultID); err != nil {
			return err
		}
	}

	return tx.Commit(context.Background())
}

func (s *Store) releaseHelper(tx pgx.Tx, id, resultID string) error {
	_, err := tx.Exec(context.Background(), `DELETE FROM cache_results WHERE id = $1 AND result_id = $2`, id, resultID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(context.Background(), `DELETE FROM cache_by_result WHERE result_id = $1 AND id = $2`, resultID, id)
	if err != nil {
		return err
	}

	var resultCount int
	if err := tx.QueryRow(context.Background(), `SELECT COUNT(*) FROM cache_results WHERE id = $1`, id).Scan(&resultCount); err != nil {
		return err
	}

	var linkCount int
	if err := tx.QueryRow(context.Background(), `SELECT COUNT(*) FROM cache_link_forward WHERE source_id = $1`, id).Scan(&linkCount); err != nil {
		return err
	}

	if resultCount == 0 && linkCount == 0 {
		return s.emptyBranchWithParents(tx, id)
	}
	return nil
}

func (s *Store) emptyBranchWithParents(tx pgx.Tx, id string) error {
	queue := []string{id}
	seen := map[string]struct{}{id: {}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		var resultCount, linkCount int
		if err := tx.QueryRow(context.Background(), `SELECT COUNT(*) FROM cache_results WHERE id = $1`, cur).Scan(&resultCount); err != nil {
			return err
		}
		if err := tx.QueryRow(context.Background(), `SELECT COUNT(*) FROM cache_link_forward WHERE source_id = $1`, cur).Scan(&linkCount); err != nil {
			return err
		}
		if resultCount > 0 || linkCount > 0 {
			continue
		}

		rows, err := tx.Query(context.Background(), `SELECT source_id FROM cache_backlinks WHERE target_id = $1`, cur)
		if err != nil {
			return err
		}
		var parents []string
		for rows.Next() {
			var parent string
			if err := rows.Scan(&parent); err != nil {
				rows.Close()
				return err
			}
			parents = append(parents, parent)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, parent := range parents {
			_, err := tx.Exec(context.Background(), `
				DELETE FROM cache_link_forward
				WHERE source_id = $1 AND link_key LIKE '%@' || $2
			`, parent, cur)
			if err != nil {
				return err
			}

			var parentLinkCount int
			if err := tx.QueryRow(context.Background(), `SELECT COUNT(*) FROM cache_link_forward WHERE source_id = $1`, parent).Scan(&parentLinkCount); err != nil {
				return err
			}
			var parentResultCount int
			if err := tx.QueryRow(context.Background(), `SELECT COUNT(*) FROM cache_results WHERE id = $1`, parent).Scan(&parentResultCount); err != nil {
				return err
			}
			if parentLinkCount == 0 && parentResultCount == 0 {
				if _, ok := seen[parent]; !ok {
					seen[parent] = struct{}{}
					queue = append(queue, parent)
				}
			}
		}

		_, _ = tx.Exec(context.Background(), `DELETE FROM cache_backlinks WHERE target_id = $1`, cur)
		_, _ = tx.Exec(context.Background(), `DELETE FROM cache_link_forward WHERE source_id = $1`, cur)
		_, _ = tx.Exec(context.Background(), `DELETE FROM cache_links WHERE id = $1`, cur)
	}
	return nil
}

func (s *Store) AddLink(id string, link solver.CacheInfoLink, target string) error {
	tx, err := s.pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())

	scopedID := s.scopeID(id)
	scopedTarget := s.scopeID(target)
	_, err = tx.Exec(context.Background(), `INSERT INTO cache_links (id) VALUES ($1) ON CONFLICT DO NOTHING`, scopedID)
	if err != nil {
		return err
	}

	linkKey, err := linkKey(link, scopedTarget)
	if err != nil {
		return err
	}

	_, err = tx.Exec(context.Background(), `
		INSERT INTO cache_link_forward (source_id, link_key, target_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, scopedID, linkKey, scopedTarget)
	if err != nil {
		return err
	}

	_, err = tx.Exec(context.Background(), `
		INSERT INTO cache_backlinks (target_id, source_id) VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, scopedTarget, scopedID)
	if err != nil {
		return err
	}

	return tx.Commit(context.Background())
}

func (s *Store) WalkLinks(id string, link solver.CacheInfoLink, fn func(id string) error) error {
	dt, err := json.Marshal(link)
	if err != nil {
		return err
	}
	index := string(bytes.Join([][]byte{dt, {}}, []byte("@")))

	rows, err := s.pool.Query(context.Background(), `
		SELECT target_id FROM cache_link_forward
		WHERE source_id = $1 AND link_key LIKE $2 || '%'
	`, s.scopeID(id), index)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return err
		}
		if err := fn(s.unscopeID(target)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) HasLink(id string, link solver.CacheInfoLink, target string) bool {
	linkKey, err := linkKey(link, s.scopeID(target))
	if err != nil {
		return false
	}
	var exists bool
	err = s.pool.QueryRow(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM cache_link_forward WHERE source_id = $1 AND link_key = $2)
	`, s.scopeID(id), linkKey).Scan(&exists)
	return err == nil && exists
}

func (s *Store) WalkBacklinks(id string, fn func(id string, link solver.CacheInfoLink) error) error {
	scopedID := s.scopeID(id)
	rows, err := s.pool.Query(context.Background(), `SELECT source_id FROM cache_backlinks WHERE target_id = $1`, scopedID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type backlinkEntry struct {
		sourceID string
	}
	var sources []backlinkEntry
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return err
		}
		sources = append(sources, backlinkEntry{sourceID: sourceID})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, src := range sources {
		linkRows, err := s.pool.Query(context.Background(), `
			SELECT link_key FROM cache_link_forward WHERE source_id = $1 AND link_key LIKE '%@' || $2
		`, src.sourceID, scopedID)
		if err != nil {
			return err
		}
		for linkRows.Next() {
			var linkKey string
			if err := linkRows.Scan(&linkKey); err != nil {
				linkRows.Close()
				return err
			}
			parts := bytes.Split([]byte(linkKey), []byte("@"))
			if len(parts) != 2 {
				linkRows.Close()
				return errors.Errorf("invalid link key %s", linkKey)
			}
			var l solver.CacheInfoLink
			if err := json.Unmarshal(parts[0], &l); err != nil {
				linkRows.Close()
				return err
			}
			l.Digest = digest.FromBytes(fmt.Appendf(nil, "%s@%d", l.Digest, l.Output))
			l.Output = 0
			if err := fn(s.unscopeID(src.sourceID), l); err != nil {
				linkRows.Close()
				return err
			}
		}
		linkRows.Close()
		if err := linkRows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func linkKey(link solver.CacheInfoLink, target string) (string, error) {
	dt, err := json.Marshal(link)
	if err != nil {
		return "", err
	}
	return string(bytes.Join([][]byte{dt, []byte(target)}, []byte("@"))), nil
}
