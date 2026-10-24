package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/L1ght147/vpsdeck/internal/report"
)

var ErrNotFound = errors.New("not found")

type Server struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	CreatedAt  time.Time      `json:"created_at"`
	LastSeen   time.Time      `json:"last_seen"`
	MutedUntil time.Time      `json:"muted_until"`
	Report     *report.Report `json:"report,omitempty"`
}

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS servers (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL,
	token_hash  TEXT NOT NULL UNIQUE,
	created_at  INTEGER NOT NULL,
	last_seen   INTEGER NOT NULL DEFAULT 0,
	muted_until INTEGER NOT NULL DEFAULT 0,
	report      TEXT
);
CREATE TABLE IF NOT EXISTS alert_state (
	server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
	key       TEXT NOT NULL,
	level     INTEGER NOT NULL,
	since     INTEGER NOT NULL,
	PRIMARY KEY (server_id, key)
);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`

func OpenStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// AddServer registers a server and returns its plaintext token, which is not stored.
func (s *Store) AddServer(name string) (Server, string, error) {
	if name == "" {
		name = "server"
	}
	tok := newToken()
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO servers (name, token_hash, created_at) VALUES (?, ?, ?)`,
		name, hashToken(tok), now.Unix())
	if err != nil {
		return Server{}, "", err
	}
	id, _ := res.LastInsertId()
	return Server{ID: id, Name: name, CreatedAt: now}, tok, nil
}

func (s *Store) ServerByToken(tok string) (Server, error) {
	return s.scanOne(`WHERE token_hash = ?`, hashToken(tok))
}

func (s *Store) Server(id int64) (Server, error) {
	return s.scanOne(`WHERE id = ?`, id)
}

func (s *Store) Servers() ([]Server, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, last_seen, muted_until, report FROM servers ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

func (s *Store) scanOne(where string, arg any) (Server, error) {
	row := s.db.QueryRow(`SELECT id, name, created_at, last_seen, muted_until, report FROM servers `+where, arg)
	srv, err := scanServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return srv, err
}

type scanner interface{ Scan(dest ...any) error }

func scanServer(sc scanner) (Server, error) {
	var (
		srv                    Server
		created, seen, mutedTo int64
		rep                    sql.NullString
	)
	if err := sc.Scan(&srv.ID, &srv.Name, &created, &seen, &mutedTo, &rep); err != nil {
		return Server{}, err
	}
	srv.CreatedAt = time.Unix(created, 0)
	if seen > 0 {
		srv.LastSeen = time.Unix(seen, 0)
	}
	if mutedTo > 0 {
		srv.MutedUntil = time.Unix(mutedTo, 0)
	}
	if rep.Valid && rep.String != "" {
		var r report.Report
		if err := json.Unmarshal([]byte(rep.String), &r); err == nil {
			srv.Report = &r
		}
	}
	return srv, nil
}

// SaveReport stores the latest report. The agent's name wins once it reports,
// so renaming a server is done in its agent config.
func (s *Store) SaveReport(id int64, r *report.Report, at time.Time) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE servers SET report = ?, last_seen = ?, name = CASE WHEN ? <> '' THEN ? ELSE name END WHERE id = ?`,
		string(b), at.Unix(), r.Name, r.Name, id)
	return err
}

func (s *Store) DeleteServer(id int64) error {
	res, err := s.db.Exec(`DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Mute(id int64, until time.Time) error {
	_, err := s.db.Exec(`UPDATE servers SET muted_until = ? WHERE id = ?`, until.Unix(), id)
	return err
}

// AlertLevels returns the stored alert levels for a server, keyed by alert key.
func (s *Store) AlertLevels(id int64) (map[string]Level, error) {
	rows, err := s.db.Query(`SELECT key, level FROM alert_state WHERE server_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Level{}
	for rows.Next() {
		var k string
		var l Level
		if err := rows.Scan(&k, &l); err != nil {
			return nil, err
		}
		out[k] = l
	}
	return out, rows.Err()
}

func (s *Store) SetAlertLevel(id int64, key string, l Level) error {
	if l == LevelOK {
		_, err := s.db.Exec(`DELETE FROM alert_state WHERE server_id = ? AND key = ?`, id, key)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO alert_state (server_id, key, level, since) VALUES (?, ?, ?, ?)
		ON CONFLICT(server_id, key) DO UPDATE SET level = excluded.level`, id, key, l, time.Now().Unix())
	return err
}

func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
