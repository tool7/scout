CREATE TABLE files (
  project     TEXT NOT NULL,
  path        TEXT NOT NULL,
  blob_hash   TEXT NOT NULL,
  size_bytes  INTEGER NOT NULL,
  language    TEXT,
  content     TEXT NOT NULL,
  indexed_at  TEXT NOT NULL,
  PRIMARY KEY (project, path)
);

CREATE INDEX idx_files_blob_hash ON files (blob_hash);

CREATE VIRTUAL TABLE files_fts USING fts5(
  path,
  content,
  content       = 'files',
  content_rowid = 'rowid',
  tokenize      = 'trigram'
);

CREATE TRIGGER files_ai AFTER INSERT ON files BEGIN
  INSERT INTO files_fts(rowid, path, content)
  VALUES (new.rowid, new.path, new.content);
END;

CREATE TRIGGER files_ad AFTER DELETE ON files BEGIN
  INSERT INTO files_fts(files_fts, rowid, path, content)
  VALUES ('delete', old.rowid, old.path, old.content);
END;

CREATE TRIGGER files_au AFTER UPDATE ON files BEGIN
  INSERT INTO files_fts(files_fts, rowid, path, content)
  VALUES ('delete', old.rowid, old.path, old.content);
  INSERT INTO files_fts(rowid, path, content)
  VALUES (new.rowid, new.path, new.content);
END;

ALTER TABLE sync_state ADD COLUMN file_count INTEGER;
