// Package db provides SQLite database storage for AirFi sessions.
package db

import (
	"database/sql"
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
}

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
			settled_at DATETIME
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
			sender_address TEXT DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
		CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at);
		CREATE INDEX IF NOT EXISTS idx_wallets_status ON guest_wallets(status);
		CREATE INDEX IF NOT EXISTS idx_wallets_address ON guest_wallets(address);
	`)
	return err
}

// CreateSession inserts a new session.
func (db *DB) CreateSession(s *Session) error {
	_, err := db.conn.Exec(`
		INSERT INTO sessions (id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.ID, s.WalletID, s.ChannelID, s.GuestAddress, s.HostAddress, s.FundingCKB, s.BalanceCKB, s.SpentCKB, s.CreatedAt, s.ExpiresAt, s.Status, s.SettledAt)
	return err
}

// GetSession retrieves a session by ID.
func (db *DB) GetSession(id string) (*Session, error) {
	row := db.conn.QueryRow(`
		SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at
		FROM sessions WHERE id = ?
	`, id)

	s := &Session{}
	var walletID, channelID, hostAddress sql.NullString
	var settledAt sql.NullTime
	err := row.Scan(&s.ID, &walletID, &channelID, &s.GuestAddress, &hostAddress, &s.FundingCKB, &s.BalanceCKB, &s.SpentCKB, &s.CreatedAt, &s.ExpiresAt, &s.Status, &settledAt)
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
	return s, nil
}

// GetSessionByWalletID retrieves a session by wallet ID.
func (db *DB) GetSessionByWalletID(walletID string) (*Session, error) {
	row := db.conn.QueryRow(`
		SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at
		FROM sessions WHERE wallet_id = ?
	`, walletID)

	s := &Session{}
	var wID, channelID, hostAddress sql.NullString
	var settledAt sql.NullTime
	err := row.Scan(&s.ID, &wID, &channelID, &s.GuestAddress, &hostAddress, &s.FundingCKB, &s.BalanceCKB, &s.SpentCKB, &s.CreatedAt, &s.ExpiresAt, &s.Status, &settledAt)
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
	return s, nil
}

// ListSessions returns all sessions, optionally filtered by status.
func (db *DB) ListSessions(status string) ([]*Session, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = db.conn.Query(`
			SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at
			FROM sessions WHERE status = ? ORDER BY created_at DESC
		`, status)
	} else {
		rows, err = db.conn.Query(`
			SELECT id, wallet_id, channel_id, guest_address, host_address, funding_ckb, balance_ckb, spent_ckb, created_at, expires_at, status, settled_at
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
		var walletID, channelID, hostAddress sql.NullString
		var settledAt sql.NullTime
		if err := rows.Scan(&s.ID, &walletID, &channelID, &s.GuestAddress, &hostAddress, &s.FundingCKB, &s.BalanceCKB, &s.SpentCKB, &s.CreatedAt, &s.ExpiresAt, &s.Status, &settledAt); err != nil {
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
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// UpdateSessionStatus updates the status of a session.
func (db *DB) UpdateSessionStatus(id, status string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, status, id)
	return err
}

// UpdateSessionChannel updates the channel ID and status.
func (db *DB) UpdateSessionChannel(id, channelID, status string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET channel_id = ?, status = ? WHERE id = ?`, channelID, status, id)
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
		INSERT INTO guest_wallets (id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, w.ID, w.Address, w.PrivateKeyHex, w.FundingCKB, w.BalanceCKB, w.CreatedAt, w.FundedAt, w.SessionID, w.Status, w.SenderAddress)
	return err
}

// GetGuestWallet retrieves a guest wallet by ID.
func (db *DB) GetGuestWallet(id string) (*GuestWallet, error) {
	row := db.conn.QueryRow(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address
		FROM guest_wallets WHERE id = ?
	`, id)

	w := &GuestWallet{}
	var fundedAt sql.NullTime
	var sessionID, senderAddr sql.NullString
	err := row.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessionID, &w.Status, &senderAddr)
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
	return w, nil
}

// GetGuestWalletByAddress retrieves a guest wallet by CKB address.
func (db *DB) GetGuestWalletByAddress(address string) (*GuestWallet, error) {
	row := db.conn.QueryRow(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address
		FROM guest_wallets WHERE address = ?
	`, address)

	w := &GuestWallet{}
	var fundedAt sql.NullTime
	var sessionID, senderAddr sql.NullString
	err := row.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessionID, &w.Status, &senderAddr)
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
	return w, nil
}

// ListPendingWallets returns wallets waiting for funding.
func (db *DB) ListPendingWallets() ([]*GuestWallet, error) {
	rows, err := db.conn.Query(`
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address
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
		var sessionID, senderAddr sql.NullString
		if err := rows.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessionID, &w.Status, &senderAddr); err != nil {
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
		SELECT id, address, private_key_hex, funding_ckb, balance_ckb, created_at, funded_at, session_id, status, sender_address
		FROM guest_wallets WHERE session_id = ?
	`, sessionID)

	w := &GuestWallet{}
	var fundedAt sql.NullTime
	var sessID, senderAddr sql.NullString
	err := row.Scan(&w.ID, &w.Address, &w.PrivateKeyHex, &w.FundingCKB, &w.BalanceCKB, &w.CreatedAt, &fundedAt, &sessID, &w.Status, &senderAddr)
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
