// Package db provides SQLite database storage for AirFi sessions.
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB represents the database connection.
type DB struct {
	conn *sql.DB
}

// Session represents a WiFi session record.
type Session struct {
	ID           string
	WalletID     string // Guest wallet ID
	ChannelID    string // Perun channel ID
	GuestAddress string
	HostAddress  string
	FundingCKB   int64     // Initial funding amount
	BalanceCKB   int64     // Current remaining balance
	SpentCKB     int64     // Total spent on micropayments
	CreatedAt    time.Time
	ExpiresAt    time.Time
	Status       string // pending_funding, funding_detected, channel_open, active, settled, expired
	SettledAt    *time.Time
	MACAddress   string // Guest device MAC address
	IPAddress    string // Guest device IP address
}

// GuestWallet represents a generated guest wallet.
type GuestWallet struct {
	ID            string
	Address       string
	PrivateKeyHex string // Encrypted or hex-encoded private key
	FundingCKB    int64  // Required funding amount
	BalanceCKB    int64  // Current on-chain balance
	CreatedAt     time.Time
	FundedAt      *time.Time
	SessionID     string // Associated session after funding
	Status        string // created, funded, channel_open, settled, withdrawn
	SenderAddress string // Original sender address for refund
	MACAddress    string // Guest device MAC address (from captive portal)
	IPAddress     string // Guest device IP address (from captive portal)
}

// Settings represents configurable system settings.
type Settings struct {
	Key   string
	Value string
}

// Default settings values
const (
	DefaultRatePerHour = 500 // 500 CKB per hour (in CKB, not shannons)
)

// Open opens the SQLite database and creates tables if needed.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	// Create tables
	if err := createTables(conn); err != nil {
		conn.Close()
		return nil, err
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

func createTables(conn *sql.DB) error {
	_, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			wallet_id TEXT,
			channel_id TEXT,
			guest_address TEXT,
			host_address TEXT,
			funding_ckb INTEGER DEFAULT 0,
			balance_ckb INTEGER DEFAULT 0,
			spent_ckb INTEGER DEFAULT 0,
			created_at DATETIME,
			expires_at DATETIME,
			status TEXT DEFAULT 'pending_funding',
			settled_at DATETIME,
			mac_address TEXT DEFAULT '',
			ip_address TEXT DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS guest_wallets (
			id TEXT PRIMARY KEY,
			address TEXT UNIQUE,
			private_key_hex TEXT,
			funding_ckb INTEGER DEFAULT 0,
			balance_ckb INTEGER DEFAULT 0,
			created_at DATETIME,
			funded_at DATETIME,
			session_id TEXT,
			status TEXT DEFAULT 'created',
			sender_address TEXT DEFAULT '',
			mac_address TEXT DEFAULT '',
			ip_address TEXT DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
		CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at);
		CREATE INDEX IF NOT EXISTS idx_wallets_status ON guest_wallets(status);
		CREATE INDEX IF NOT EXISTS idx_wallets_address ON guest_wallets(address);
	`)
	if err != nil {
		return err
	}

	// Initialize default settings if not exist
	_, err = conn.Exec(`
		INSERT OR IGNORE INTO settings (key, value) VALUES ('rate_per_hour', '500')
	`)
	return err
}

// CreateSession inserts a new session.
func (db *DB) CreateSession(s *Session) error {
	_, err := db.conn.Exec(`
		INSERT INTO sessions (id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at, mac_address, ip_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.ID, s.WalletID, s.ChannelID, s.GuestAddress, s.HostAddress, s.FundingCKB, s.BalanceCKB, s.SpentCKB, s.CreatedAt, s.ExpiresAt, s.Status, s.SettledAt, s.MACAddress, s.IPAddress)
	return err
}

// GetSession retrieves a session by ID.
func (db *DB) GetSession(id string) (*Session, error) {
	row := db.conn.QueryRow(`
		SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at, mac_address, ip_address
		FROM sessions WHERE id = ?
	`, id)

	s := &Session{}
	var walletID, channelID, hostAddress, macAddr, ipAddr sql.NullString
	var settledAt sql.NullTime
	err := row.Scan(&s.ID, &walletID, &channelID, &s.GuestAddress, &hostAddress, &s.FundingCKB, &s.BalanceCKB, &s.SpentCKB, &s.CreatedAt, &s.ExpiresAt, &s.Status, &settledAt, &macAddr, &ipAddr)
	if err != nil {
		return nil, err
	}
	if walletID.Valid {
		s.WalletID = walletID.String
	}
	if channelID.Valid {
		s.ChannelID = channelID.String
	}
	if hostAddress.Valid {
		s.HostAddress = hostAddress.String
	}
	if settledAt.Valid {
		s.SettledAt = &settledAt.Time
	}
	if macAddr.Valid {
		s.MACAddress = macAddr.String
	}
	if ipAddr.Valid {
		s.IPAddress = ipAddr.String
	}
	return s, nil
}

// GetSessionByWalletID retrieves a session by wallet ID.
func (db *DB) GetSessionByWalletID(walletID string) (*Session, error) {
	row := db.conn.QueryRow(`
		SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at, mac_address, ip_address
		FROM sessions WHERE wallet_id = ?
	`, walletID)

	s := &Session{}
	var wID, channelID, hostAddress, macAddr, ipAddr sql.NullString
	var settledAt sql.NullTime
	err := row.Scan(&s.ID, &wID, &channelID, &s.GuestAddress, &hostAddress, &s.FundingCKB, &s.BalanceCKB, &s.SpentCKB, &s.CreatedAt, &s.ExpiresAt, &s.Status, &settledAt, &macAddr, &ipAddr)
	if err != nil {
		return nil, err
	}
	if wID.Valid {
		s.WalletID = wID.String
	}
	if channelID.Valid {
		s.ChannelID = channelID.String
	}
	if hostAddress.Valid {
		s.HostAddress = hostAddress.String
	}
	if settledAt.Valid {
		s.SettledAt = &settledAt.Time
	}
	if macAddr.Valid {
		s.MACAddress = macAddr.String
	}
	if ipAddr.Valid {
		s.IPAddress = ipAddr.String
	}
	return s, nil
}

// ListSessions returns all sessions, optionally filtered by status.
func (db *DB) ListSessions(status string) ([]*Session, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = db.conn.Query(`
			SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at, mac_address, ip_address
			FROM sessions WHERE status = ? ORDER BY created_at DESC
		`, status)
	} else {
		rows, err = db.conn.Query(`
			SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at, mac_address, ip_address
			FROM sessions ORDER BY created_at DESC
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		s := &Session{}
		var walletID, channelID, hostAddress, macAddr, ipAddr sql.NullString
		var settledAt sql.NullTime
		if err := rows.Scan(&s.ID, &walletID, &channelID, &s.GuestAddress, &hostAddress, &s.FundingCKB, &s.BalanceCKB, &s.SpentCKB, &s.CreatedAt, &s.ExpiresAt, &s.Status, &settledAt, &macAddr, &ipAddr); err != nil {
			return nil, err
		}
		if walletID.Valid {
			s.WalletID = walletID.String
		}
		if channelID.Valid {
			s.ChannelID = channelID.String
		}
		if hostAddress.Valid {
			s.HostAddress = hostAddress.String
		}
		if settledAt.Valid {
			s.SettledAt = &settledAt.Time
		}
		if macAddr.Valid {
			s.MACAddress = macAddr.String
		}
		if ipAddr.Valid {
			s.IPAddress = ipAddr.String
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// UpdateSessionStatus updates the status of a session.
func (db *DB) UpdateSessionStatus(id, status string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, status, id)
	return err
}

// UpdateSessionMAC updates the MAC and IP address of a session.
func (db *DB) UpdateSessionMAC(id, macAddress, ipAddress string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET mac_address = ?, ip_address = ? WHERE id = ?`, macAddress, ipAddress, id)
	return err
}

// UpdateSessionChannel updates the channel ID and status.
func (db *DB) UpdateSessionChannel(id, channelID, status string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET channel_id = ?, status = ? WHERE id = ?`, channelID, status, id)
	return err
}

// UpdateSessionChannelAndActivate updates channel ID, status to active, and starts the timer.
// This should be called when the Perun channel is successfully opened.
func (db *DB) UpdateSessionChannelAndActivate(id, channelID string, duration time.Duration) error {
	expiresAt := time.Now().Add(duration)
	_, err := db.conn.Exec(`UPDATE sessions SET channel_id = ?, status = 'active', expires_at = ? WHERE id = ?`, channelID, expiresAt, id)
	return err
}

// UpdateSessionBalance updates the balance and spent amount.
func (db *DB) UpdateSessionBalance(id string, balanceCKB, spentCKB int64) error {
	_, err := db.conn.Exec(`UPDATE sessions SET balance_ckb = ?, spent_ckb = ? WHERE id = ?`, balanceCKB, spentCKB, id)
	return err
}

// SettleSession marks a session as settled.
func (db *DB) SettleSession(id string) error {
	now := time.Now()
	_, err := db.conn.Exec(`UPDATE sessions SET status = 'settled', settled_at = ? WHERE id = ?`, now, id)
	return err
}

// CreateGuestWallet inserts a new guest wallet.
func (db *DB) CreateGuestWallet(w *GuestWallet) error {
	_, err := db.conn.Exec(`
		INSERT INTO guest_wallets (id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address, mac_address, ip_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, w.ID, w.Address, w.PrivateKeyHex, w.FundingCKB, w.BalanceCKB, w.CreatedAt, w.FundedAt, w.SessionID, w.Status, w.SenderAddress, w.MACAddress, w.IPAddress)
	return err
}

// GetGuestWallet retrieves a guest wallet by ID.
func (db *DB) GetGuestWallet(id string) (*GuestWallet, error) {
	row := db.conn.QueryRow(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address, mac_address, ip_address
		FROM guest_wallets WHERE id = ?
	`, id)

	w := &GuestWallet{}
	var fundedAt sql.NullTime
	var sessionID, senderAddr, macAddr, ipAddr sql.NullString
	err := row.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessionID, &w.Status, &senderAddr, &macAddr, &ipAddr)
	if err != nil {
		return nil, err
	}
	if fundedAt.Valid {
		w.FundedAt = &fundedAt.Time
	}
	if sessionID.Valid {
		w.SessionID = sessionID.String
	}
	if senderAddr.Valid {
		w.SenderAddress = senderAddr.String
	}
	if macAddr.Valid {
		w.MACAddress = macAddr.String
	}
	if ipAddr.Valid {
		w.IPAddress = ipAddr.String
	}
	return w, nil
}

// GetGuestWalletByAddress retrieves a guest wallet by CKB address.
func (db *DB) GetGuestWalletByAddress(address string) (*GuestWallet, error) {
	row := db.conn.QueryRow(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address, mac_address, ip_address
		FROM guest_wallets WHERE address = ?
	`, address)

	w := &GuestWallet{}
	var fundedAt sql.NullTime
	var sessionID, senderAddr, macAddr, ipAddr sql.NullString
	err := row.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessionID, &w.Status, &senderAddr, &macAddr, &ipAddr)
	if err != nil {
		return nil, err
	}
	if fundedAt.Valid {
		w.FundedAt = &fundedAt.Time
	}
	if sessionID.Valid {
		w.SessionID = sessionID.String
	}
	if senderAddr.Valid {
		w.SenderAddress = senderAddr.String
	}
	if macAddr.Valid {
		w.MACAddress = macAddr.String
	}
	if ipAddr.Valid {
		w.IPAddress = ipAddr.String
	}
	return w, nil
}

// ListPendingWallets returns wallets waiting for funding.
func (db *DB) ListPendingWallets() ([]*GuestWallet, error) {
	rows, err := db.conn.Query(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address, mac_address, ip_address
		FROM guest_wallets WHERE status = 'created' ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var wallets []*GuestWallet
	for rows.Next() {
		w := &GuestWallet{}
		var fundedAt sql.NullTime
		var sessionID, senderAddr, macAddr, ipAddr sql.NullString
		if err := rows.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessionID, &w.Status, &senderAddr, &macAddr, &ipAddr); err != nil {
			return nil, err
		}
		if fundedAt.Valid {
			w.FundedAt = &fundedAt.Time
		}
		if sessionID.Valid {
			w.SessionID = sessionID.String
		}
		if senderAddr.Valid {
			w.SenderAddress = senderAddr.String
		}
		if macAddr.Valid {
			w.MACAddress = macAddr.String
		}
		if ipAddr.Valid {
			w.IPAddress = ipAddr.String
		}
		wallets = append(wallets, w)
	}
	return wallets, nil
}

// UpdateWalletFunded marks a wallet as funded.
func (db *DB) UpdateWalletFunded(id string, balanceCKB int64, sessionID string) error {
	now := time.Now()
	_, err := db.conn.Exec(`
		UPDATE guest_wallets SET balance_ckb = ?, funded_at = ?, session_id = ?, status = 'funded' WHERE id = ?
	`, balanceCKB, now, sessionID, id)
	return err
}

// UpdateWalletBalance updates the wallet balance without changing status.
func (db *DB) UpdateWalletBalance(id string, balanceCKB int64) error {
	_, err := db.conn.Exec(`UPDATE guest_wallets SET balance_ckb = ? WHERE id = ?`, balanceCKB, id)
	return err
}

// UpdateWalletStatus updates the wallet status.
func (db *DB) UpdateWalletStatus(id, status string) error {
	_, err := db.conn.Exec(`UPDATE guest_wallets SET status = ? WHERE id = ?`, status, id)
	return err
}

// UpdateWalletSenderAddress updates the sender address for refund.
func (db *DB) UpdateWalletSenderAddress(id, senderAddress string) error {
	_, err := db.conn.Exec(`UPDATE guest_wallets SET sender_address = ? WHERE id = ?`, senderAddress, id)
	return err
}

// GetWalletBySessionID retrieves a guest wallet by session ID.
func (db *DB) GetWalletBySessionID(sessionID string) (*GuestWallet, error) {
	row := db.conn.QueryRow(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address, mac_address, ip_address
		FROM guest_wallets WHERE session_id = ?
	`, sessionID)

	w := &GuestWallet{}
	var fundedAt sql.NullTime
	var sessID, senderAddr, macAddr, ipAddr sql.NullString
	err := row.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessID, &w.Status, &senderAddr, &macAddr, &ipAddr)
	if err != nil {
		return nil, err
	}
	if fundedAt.Valid {
		w.FundedAt = &fundedAt.Time
	}
	if sessID.Valid {
		w.SessionID = sessID.String
	}
	if senderAddr.Valid {
		w.SenderAddress = senderAddr.String
	}
	if macAddr.Valid {
		w.MACAddress = macAddr.String
	}
	if ipAddr.Valid {
		w.IPAddress = ipAddr.String
	}
	return w, nil
}

// GetStats returns session statistics.
func (db *DB) GetStats() (total int, active int, totalEarned int64, err error) {
	row := db.conn.QueryRow(`SELECT COUNT(*) FROM sessions`)
	if err = row.Scan(&total); err != nil {
		return
	}

	row = db.conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE status = 'active'`)
	if err = row.Scan(&active); err != nil {
		return
	}

	row = db.conn.QueryRow(`SELECT COALESCE(SUM(spent_ckb), 0) FROM sessions`)
	err = row.Scan(&totalEarned)
	return
}

// ExtendSession extends the session expiry time and updates balances.
func (db *DB) ExtendSession(id string, additionalMinutes int64, spentCKB int64) error {
	_, err := db.conn.Exec(`
		UPDATE sessions
		SET expires_at = datetime(expires_at, '+' || ? || ' minutes'),
		    spent_ckb = spent_ckb + ?,
		    balance_ckb = balance_ckb - ?
		WHERE id = ?
	`, additionalMinutes, spentCKB, spentCKB, id)
	return err
}

// CleanupExpired marks expired sessions.
func (db *DB) CleanupExpired() (int64, error) {
	result, err := db.conn.Exec(`
		UPDATE sessions SET status = 'expired'
		WHERE status = 'active' AND balance_ckb <= 0
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetSetting retrieves a setting value by key.
func (db *DB) GetSetting(key string) (string, error) {
	row := db.conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, key)
	var value string
	err := row.Scan(&value)
	return value, err
}

// SetSetting sets a setting value.
func (db *DB) SetSetting(key, value string) error {
	_, err := db.conn.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// GetRatePerHour returns the configured rate per hour in CKB.
func (db *DB) GetRatePerHour() (int64, error) {
	value, err := db.GetSetting("rate_per_hour")
	if err != nil {
		return DefaultRatePerHour, nil // Return default if not found
	}
	var rate int64
	_, err = fmt.Sscanf(value, "%d", &rate)
	if err != nil {
		return DefaultRatePerHour, nil
	}
	return rate, nil
}

// SetRatePerHour sets the rate per hour in CKB.
func (db *DB) SetRatePerHour(rate int64) error {
	return db.SetSetting("rate_per_hour", fmt.Sprintf("%d", rate))
}

// GetAllSettings returns all settings as a map.
func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.conn.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, nil
}
