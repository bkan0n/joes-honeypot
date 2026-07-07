package store

import (
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

func (s *Store) GetConfig(guildID snowflake.ID) (*Config, error) {
	var (
		logCh  sql.NullInt64
		action string
	)
	err := s.db.QueryRow(
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

func (s *Store) UpsertConfig(cfg Config) error {
	_, err := s.db.Exec(`INSERT INTO honeypot_config (guild_id, log_channel_id, action) VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET log_channel_id = excluded.log_channel_id, action = excluded.action`,
		int64(cfg.GuildID), nullID(cfg.LogChannelID), string(cfg.Action))
	return err
}

func scanChannel(row *sql.Row) (*Channel, error) {
	var (
		chID, gID int64
		msgID     sql.NullInt64
	)
	err := row.Scan(&chID, &gID, &msgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Channel{ChannelID: snowflake.ID(chID), GuildID: snowflake.ID(gID), MsgID: idPtr(msgID)}, nil
}

func (s *Store) GetChannel(guildID snowflake.ID) (*Channel, error) {
	return scanChannel(s.db.QueryRow(
		`SELECT channel_id, guild_id, msg_id FROM honeypot_channels WHERE guild_id = ?`, int64(guildID)))
}

func (s *Store) GetChannelByID(channelID snowflake.ID) (*Channel, error) {
	return scanChannel(s.db.QueryRow(
		`SELECT channel_id, guild_id, msg_id FROM honeypot_channels WHERE channel_id = ?`, int64(channelID)))
}

func (s *Store) SetChannel(guildID, channelID snowflake.ID) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM honeypot_channels WHERE guild_id = ? AND channel_id != ?`,
		int64(guildID), int64(channelID))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	_, err = tx.Exec(`INSERT INTO honeypot_channels (channel_id, guild_id) VALUES (?, ?)
		ON CONFLICT(channel_id) DO NOTHING`, int64(channelID), int64(guildID))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) SetWarningMsg(channelID snowflake.ID, msgID *snowflake.ID) error {
	_, err := s.db.Exec(`UPDATE honeypot_channels SET msg_id = ? WHERE channel_id = ?`,
		nullID(msgID), int64(channelID))
	return err
}

func (s *Store) ClearWarningMsgByMsgID(msgID snowflake.ID) error {
	_, err := s.db.Exec(`UPDATE honeypot_channels SET msg_id = NULL WHERE msg_id = ?`, int64(msgID))
	return err
}

func (s *Store) UnsetLogChannel(guildID snowflake.ID) error {
	_, err := s.db.Exec(`UPDATE honeypot_config SET log_channel_id = NULL WHERE guild_id = ?`, int64(guildID))
	return err
}

func (s *Store) RemoveChannel(channelID snowflake.ID) error {
	_, err := s.db.Exec(`DELETE FROM honeypot_channels WHERE channel_id = ?`, int64(channelID))
	return err
}

func (s *Store) DeleteGuild(guildID snowflake.ID) error {
	_, err := s.db.Exec(`DELETE FROM honeypot_config WHERE guild_id = ?`, int64(guildID))
	return err
}

func (s *Store) RecordEvent(guildID, userID, channelID snowflake.ID) error {
	_, err := s.db.Exec(`INSERT INTO honeypot_events (guild_id, user_id, channel_id) VALUES (?, ?, ?)`,
		int64(guildID), int64(userID), int64(channelID))
	return err
}

func (s *Store) CountEventsByGuild(guildID snowflake.ID) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM honeypot_events WHERE guild_id = ?`, int64(guildID)).Scan(&n)
	return n, err
}
