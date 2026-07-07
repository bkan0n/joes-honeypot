package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/disgoorg/snowflake/v2"
)

type Action string

const (
	ActionSoftban  Action = "softban"
	ActionBan      Action = "ban"
	ActionDisabled Action = "disabled"
)

type Config struct {
	GuildID      snowflake.ID
	LogChannelID *snowflake.ID
	Action       Action
}

type Channel struct {
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	MsgID     *snowflake.ID
}

func nullID(id *snowflake.ID) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*id), Valid: true}
}

func idPtr(n sql.NullInt64) *snowflake.ID {
	if !n.Valid {
		return nil
	}
	id := snowflake.ID(n.Int64)
	return &id
}

func (s *Store) GetConfig(ctx context.Context, guildID snowflake.ID) (*Config, error) {
	var (
		logCh  sql.NullInt64
		action string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT log_channel_id, action FROM honeypot_config WHERE guild_id = ?`, int64(guildID),
	).Scan(&logCh, &action)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Config{GuildID: guildID, LogChannelID: idPtr(logCh), Action: Action(action)}, nil
}

func (s *Store) UpsertConfig(ctx context.Context, cfg Config) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO honeypot_config (guild_id, log_channel_id, action) VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET log_channel_id = excluded.log_channel_id, action = excluded.action`,
		int64(cfg.GuildID), nullID(cfg.LogChannelID), string(cfg.Action))
	return err
}

// loadChannels reads the full honeypot_channels table into the map that
// backs all channel reads (see Store.channels).
func loadChannels(db *sql.DB) (map[snowflake.ID]Channel, error) {
	rows, err := db.Query(`SELECT channel_id, guild_id, msg_id FROM honeypot_channels`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[snowflake.ID]Channel{}
	for rows.Next() {
		var (
			chID, gID int64
			msgID     sql.NullInt64
		)
		if err := rows.Scan(&chID, &gID, &msgID); err != nil {
			return nil, err
		}
		out[snowflake.ID(chID)] = Channel{ChannelID: snowflake.ID(chID), GuildID: snowflake.ID(gID), MsgID: idPtr(msgID)}
	}
	return out, rows.Err()
}

// cloneChannel copies a cached Channel, giving the caller its own MsgID
// pointer so cache entries are never aliased outside the lock.
func cloneChannel(c Channel) *Channel {
	out := c
	if c.MsgID != nil {
		id := *c.MsgID
		out.MsgID = &id
	}
	return &out
}

// Channel reads are served from the in-memory mirror; a miss is authoritative
// (the full table is preloaded at Open), so there is no DB fallback and the
// error is always nil. The signatures keep the error to match the rest of the
// store API.

func (s *Store) GetChannel(guildID snowflake.ID) (*Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.channels {
		if c.GuildID == guildID {
			return cloneChannel(c), nil
		}
	}
	return nil, nil
}

func (s *Store) GetChannelByID(channelID snowflake.ID) (*Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.channels[channelID]
	if !ok {
		return nil, nil
	}
	return cloneChannel(c), nil
}

func (s *Store) AllChannels() ([]Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Channel, 0, len(s.channels))
	for _, c := range s.channels {
		out = append(out, *cloneChannel(c))
	}
	return out, nil
}

// setChannelTx makes channelID the guild's only honeypot channel row,
// keeping its msg_id if the row already exists.
func setChannelTx(ctx context.Context, tx *sql.Tx, guildID, channelID snowflake.ID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM honeypot_channels WHERE guild_id = ? AND channel_id != ?`,
		int64(guildID), int64(channelID))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO honeypot_channels (channel_id, guild_id) VALUES (?, ?)
		ON CONFLICT(channel_id) DO NOTHING`, int64(channelID), int64(guildID))
	return err
}

// applyChannelSwap mirrors setChannelTx's effect into the channel map after
// the transaction committed.
func (s *Store) applyChannelSwap(guildID, channelID snowflake.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.channels {
		if c.GuildID == guildID && id != channelID {
			delete(s.channels, id)
		}
	}
	if _, ok := s.channels[channelID]; !ok {
		s.channels[channelID] = Channel{ChannelID: channelID, GuildID: guildID}
	}
}

func (s *Store) SetChannel(ctx context.Context, guildID, channelID snowflake.ID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := setChannelTx(ctx, tx, guildID, channelID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.applyChannelSwap(guildID, channelID)
	return nil
}

// SaveGuildSetup atomically upserts the guild's config and makes channelID
// its honeypot channel — one transaction, so a failure can't leave the
// config changed but the channel not (config goes first: the channel row's
// FK references it).
func (s *Store) SaveGuildSetup(ctx context.Context, cfg Config, channelID snowflake.ID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO honeypot_config (guild_id, log_channel_id, action) VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET log_channel_id = excluded.log_channel_id, action = excluded.action`,
		int64(cfg.GuildID), nullID(cfg.LogChannelID), string(cfg.Action))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := setChannelTx(ctx, tx, cfg.GuildID, channelID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.applyChannelSwap(cfg.GuildID, channelID)
	return nil
}

func (s *Store) SetWarningMsg(ctx context.Context, channelID snowflake.ID, msgID *snowflake.ID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE honeypot_channels SET msg_id = ? WHERE channel_id = ?`,
		nullID(msgID), int64(channelID))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.channels[channelID]; ok {
		c.MsgID = nil
		if msgID != nil {
			id := *msgID
			c.MsgID = &id
		}
		s.channels[channelID] = c
	}
	return nil
}

func (s *Store) ClearWarningMsgByMsgID(ctx context.Context, msgID snowflake.ID) error {
	// The map is authoritative, and this runs for every message deleted in
	// every guild — skip the DB write unless msgID is a known warning
	// message (bulk purges would otherwise become write storms).
	s.mu.RLock()
	known := false
	for _, c := range s.channels {
		if c.MsgID != nil && *c.MsgID == msgID {
			known = true
			break
		}
	}
	s.mu.RUnlock()
	if !known {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE honeypot_channels SET msg_id = NULL WHERE msg_id = ?`, int64(msgID))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.channels {
		if c.MsgID != nil && *c.MsgID == msgID {
			c.MsgID = nil
			s.channels[id] = c
		}
	}
	return nil
}

func (s *Store) UnsetLogChannel(ctx context.Context, guildID snowflake.ID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE honeypot_config SET log_channel_id = NULL WHERE guild_id = ?`, int64(guildID))
	return err
}

func (s *Store) RemoveChannel(ctx context.Context, channelID snowflake.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM honeypot_channels WHERE channel_id = ?`, int64(channelID))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.channels, channelID)
	return nil
}

func (s *Store) DeleteGuild(ctx context.Context, guildID snowflake.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM honeypot_config WHERE guild_id = ?`, int64(guildID))
	if err != nil {
		return err
	}
	// The FK cascade wiped the guild's channel rows; sweep the mirror too.
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.channels {
		if c.GuildID == guildID {
			delete(s.channels, id)
		}
	}
	return nil
}

func (s *Store) RecordEvent(ctx context.Context, guildID, userID, channelID snowflake.ID) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO honeypot_events (guild_id, user_id, channel_id) VALUES (?, ?, ?)`,
		int64(guildID), int64(userID), int64(channelID))
	return err
}

func (s *Store) CountEventsByGuild(ctx context.Context, guildID snowflake.ID) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM honeypot_events WHERE guild_id = ?`, int64(guildID)).Scan(&n)
	return n, err
}
