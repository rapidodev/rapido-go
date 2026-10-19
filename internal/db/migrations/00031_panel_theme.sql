-- +goose Up
-- The panel-wide default look (colours, depth, gauges, environment...). One
-- row, id = 1. The owner sets it from the dashboard's Theme page; every admin
-- who has not picked their own look in their browser gets this one. The JSON
-- is validated and reduced to known keys by the API (internal/httpapi/theme.go)
-- before it is stored, so the dashboard can apply it without distrust. An empty
-- object means "no panel default": the built-in look applies.
CREATE TABLE panel_theme (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    theme       JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT
);
INSERT INTO panel_theme (id) VALUES (1);

-- +goose Down
DROP TABLE panel_theme;
