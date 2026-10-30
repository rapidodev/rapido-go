-- +goose Up
-- With no action on delete, an admin whose usage was ever reset could not be
-- deleted at all: the panel answered with a bare 500.
ALTER TABLE admin_usage_logs DROP CONSTRAINT admin_usage_logs_admin_id_fkey;
ALTER TABLE admin_usage_logs ADD CONSTRAINT admin_usage_logs_admin_id_fkey
    FOREIGN KEY (admin_id) REFERENCES admins (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE admin_usage_logs DROP CONSTRAINT admin_usage_logs_admin_id_fkey;
ALTER TABLE admin_usage_logs ADD CONSTRAINT admin_usage_logs_admin_id_fkey
    FOREIGN KEY (admin_id) REFERENCES admins (id);
