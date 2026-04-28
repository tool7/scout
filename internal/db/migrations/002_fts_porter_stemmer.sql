DROP TABLE IF EXISTS commits_fts;
DROP TABLE IF EXISTS tickets_fts;

CREATE VIRTUAL TABLE commits_fts USING fts5(
  project, author, date, message, body, files,
  content = commits,
  content_rowid = rowid,
  tokenize = 'porter unicode61 remove_diacritics 2'
);

CREATE VIRTUAL TABLE tickets_fts USING fts5(
  project, summary, description, type, status, labels, comments,
  content = tickets,
  content_rowid = rowid,
  tokenize = 'porter unicode61 remove_diacritics 2'
);

INSERT INTO commits_fts (commits_fts) VALUES ('rebuild');
INSERT INTO tickets_fts (tickets_fts) VALUES ('rebuild');
