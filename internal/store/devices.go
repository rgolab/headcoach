package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

// Device is a phone or a browser paired to reach the board from another machine.
// Only a hash of its token is kept: the token itself lives in the device's cookie.
type Device struct {
	Name      string  `json:"name"`
	CreatedAt float64 `json:"created_at"`
	LastSeen  float64 `json:"last_seen"`
}

// HashToken is how a device token is stored and looked up.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// PairDevice stores a device under its name, replacing one paired before under the same name.
func (s *Store) PairDevice(name, token string) error {
	_, err := s.Exec("INSERT OR REPLACE INTO devices (name, token_hash, created_at, last_seen) VALUES (?, ?, ?, ?)",
		name, HashToken(token), Now(), Now())
	return err
}

// DeviceByToken returns the name of the device that holds this token, or "" when none does.
func (s *Store) DeviceByToken(token string) (string, error) {
	var name string
	err := s.QueryRow("SELECT name FROM devices WHERE token_hash = ?", HashToken(token)).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// SeeDevice records that a device was just seen.
func (s *Store) SeeDevice(name string) error {
	_, err := s.Exec("UPDATE devices SET last_seen = ? WHERE name = ?", Now(), name)
	return err
}

// Devices lists the paired devices, the last seen first.
func (s *Store) Devices() ([]Device, error) {
	rows, err := s.Query("SELECT name, created_at, last_seen FROM devices ORDER BY last_seen DESC, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.Name, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ForgetDevice removes a device. It reports whether there was one by that name.
func (s *Store) ForgetDevice(name string) (bool, error) {
	res, err := s.Exec("DELETE FROM devices WHERE name = ?", name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
