CREATE TABLE commits (
  id      TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  author  TEXT NOT NULL,
  date    TEXT NOT NULL,
  message TEXT NOT NULL,
  body    TEXT,
  files   TEXT
);

CREATE INDEX idx_commits_project ON commits (project);
CREATE INDEX idx_commits_date    ON commits (date);

CREATE TABLE tickets (
  id          TEXT PRIMARY KEY,
  project     TEXT NOT NULL,
  summary     TEXT NOT NULL,
  description TEXT,
  type        TEXT,
  status      TEXT,
  resolution  TEXT,
  created_at  TEXT,
  updated_at  TEXT,
  reporter    TEXT,
  assignee    TEXT,
  labels      TEXT,
  comments    TEXT
);

CREATE INDEX idx_tickets_project    ON tickets (project);
CREATE INDEX idx_tickets_status     ON tickets (status);
CREATE INDEX idx_tickets_updated_at ON tickets (updated_at);

CREATE TABLE sync_state (
  project      TEXT NOT NULL,
  source       TEXT NOT NULL,
  last_synced  TEXT NOT NULL,
  commit_count INTEGER,
  ticket_count INTEGER,
  PRIMARY KEY (project, source)
);

CREATE VIRTUAL TABLE commits_fts USING fts5(
  project, author, date, message, body, files,
  content=commits, content_rowid=rowid
);

CREATE VIRTUAL TABLE tickets_fts USING fts5(
  project, summary, description, type, status, labels, comments,
  content=tickets, content_rowid=rowid
);
