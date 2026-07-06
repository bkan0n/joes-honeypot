CREATE TABLE honeypot_config (
    guild_id       INTEGER PRIMARY KEY,
    log_channel_id INTEGER,
    action         TEXT NOT NULL DEFAULT 'softban'
);

CREATE TABLE honeypot_channels (
    channel_id INTEGER PRIMARY KEY,
    guild_id   INTEGER NOT NULL REFERENCES honeypot_config(guild_id) ON DELETE CASCADE,
    msg_id     INTEGER
);

CREATE TABLE honeypot_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id   INTEGER NOT NULL REFERENCES honeypot_config(guild_id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL,
    channel_id INTEGER REFERENCES honeypot_channels(channel_id) ON DELETE SET NULL,
    timestamp  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_channels_guild ON honeypot_channels(guild_id);
CREATE INDEX idx_events_guild ON honeypot_events(guild_id);
CREATE INDEX idx_events_user ON honeypot_events(user_id);
