CREATE TABLE index_queue (
    id         SERIAL PRIMARY KEY,
    doc_id     TEXT NOT NULL,
    action     TEXT NOT NULL DEFAULT 'index',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE OR REPLACE FUNCTION fn_idx_doc_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO index_queue (doc_id, action) VALUES (NEW.id, 'index'); RETURN NEW; END; $$;
CREATE TRIGGER idx_documents_insert AFTER INSERT ON documents FOR EACH ROW EXECUTE FUNCTION fn_idx_doc_insert();

CREATE OR REPLACE FUNCTION fn_idx_doc_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO index_queue (doc_id, action) VALUES (NEW.id, 'index'); RETURN NEW; END; $$;
CREATE TRIGGER idx_documents_update AFTER UPDATE ON documents FOR EACH ROW EXECUTE FUNCTION fn_idx_doc_update();

CREATE OR REPLACE FUNCTION fn_idx_doc_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO index_queue (doc_id, action) VALUES (OLD.id, 'delete'); RETURN OLD; END; $$;
CREATE TRIGGER idx_documents_delete AFTER DELETE ON documents FOR EACH ROW EXECUTE FUNCTION fn_idx_doc_delete();

CREATE OR REPLACE FUNCTION fn_idx_job_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO index_queue (doc_id, action) VALUES (NEW.document_id, 'index'); RETURN NEW; END; $$;
CREATE TRIGGER idx_jobs_insert AFTER INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION fn_idx_job_insert();

CREATE OR REPLACE FUNCTION fn_idx_job_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO index_queue (doc_id, action) VALUES (NEW.document_id, 'index'); RETURN NEW; END; $$;
CREATE TRIGGER idx_jobs_update AFTER UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION fn_idx_job_update();
