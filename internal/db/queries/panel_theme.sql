-- name: GetPanelTheme :one
SELECT * FROM panel_theme WHERE id = 1;

-- name: SetPanelTheme :one
UPDATE panel_theme SET theme = $1, updated_at = now(), updated_by = $2 WHERE id = 1
RETURNING *;
