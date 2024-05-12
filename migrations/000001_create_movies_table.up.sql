CREATE TABLE IF NOT EXISTS movies
(
  id         BIGSERIAL PRIMARY KEY,
  created_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT NOW(),
  title      TEXT                        NOT NULL,
  year       INTEGER                     NOT NULL,
  runtime    INTEGER                     NOT NULL,
  -- arrays in PostgreSQL are themselves queryable and indexable,
  genres     TEXT[]                      NOT NULL,
  version    INTEGER                     NOT NULL DEFAULT 1
);
