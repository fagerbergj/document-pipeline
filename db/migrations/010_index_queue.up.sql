CREATE TABLE index_queue (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    doc_id     TEXT NOT NULL,
    action     TEXT NOT NULL DEFAULT 'index',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TRIGGER idx_documents_insert AFTER INSERT ON documents
BEGIN
    INSERT INTO index_queue (doc_id, action) VALUES (NEW.id, 'index');
END;

CREATE TRIGGER idx_documents_update AFTER UPDATE ON documents
BEGIN
    INSERT INTO index_queue (doc_id, action) VALUES (NEW.id, 'index');
END;

CREATE TRIGGER idx_documents_delete AFTER DELETE ON documents
BEGIN
    INSERT INTO index_queue (doc_id, action) VALUES (OLD.id, 'delete');
END;

CREATE TRIGGER idx_jobs_insert AFTER INSERT ON jobs
BEGIN
    INSERT INTO index_queue (doc_id, action) VALUES (NEW.document_id, 'index');
END;

CREATE TRIGGER idx_jobs_update AFTER UPDATE ON jobs
BEGIN
    INSERT INTO index_queue (doc_id, action) VALUES (NEW.document_id, 'index');
END;
