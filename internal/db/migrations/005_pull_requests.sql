CREATE TABLE pull_requests (
  id              TEXT NOT NULL,
  project         TEXT NOT NULL,
  provider        TEXT NOT NULL,
  number          INTEGER NOT NULL,
  title           TEXT NOT NULL,
  body            TEXT,
  state           TEXT,
  author          TEXT,
  created_at      TEXT,
  updated_at      TEXT,
  merged_at       TEXT,
  source_branch   TEXT,
  target_branch   TEXT,
  url             TEXT,
  review_comments TEXT,
  PRIMARY KEY (project, id)
);

CREATE INDEX idx_pull_requests_project    ON pull_requests (project);
CREATE INDEX idx_pull_requests_updated_at ON pull_requests (updated_at);
CREATE INDEX idx_pull_requests_state      ON pull_requests (state);

CREATE VIRTUAL TABLE pull_requests_fts USING fts5(
  project, title, body, author, source_branch, target_branch, review_comments,
  content       = 'pull_requests',
  content_rowid = 'rowid',
  tokenize      = 'porter unicode61 remove_diacritics 2'
);

INSERT INTO pull_requests_fts (pull_requests_fts) VALUES ('rebuild');

ALTER TABLE sync_state ADD COLUMN pr_count INTEGER;
