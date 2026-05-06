DROP TABLE IF EXISTS commits_fts;

ALTER TABLE commits RENAME TO commits_old;

CREATE TABLE commits (
  id      TEXT NOT NULL,
  project TEXT NOT NULL,
  author  TEXT NOT NULL,
  date    TEXT NOT NULL,
  message TEXT NOT NULL,
  body    TEXT,
  files   TEXT,
  PRIMARY KEY (project, id)
);

INSERT INTO commits (id, project, author, date, message, body, files)
SELECT id, project, author, date, message, body, files FROM commits_old;

DROP TABLE commits_old;

CREATE INDEX idx_commits_project ON commits (project);
CREATE INDEX idx_commits_date    ON commits (date);

CREATE VIRTUAL TABLE commits_fts USING fts5(
  project, author, date, message, body, files,
  content = commits,
  content_rowid = rowid,
  tokenize = 'porter unicode61 remove_diacritics 2'
);

INSERT INTO commits_fts (commits_fts) VALUES ('rebuild');
