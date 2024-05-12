
-- This table stores the "hashed cryptographically-secure random activation tokens"
-- and "authentication tokens" that are used to verify the email addresses of users.
CREATE TABLE IF NOT EXISTS tokens
(
	-- We will only store a hash of the activation token in our 
	-- database — not the activation token itself.
	hash    BYTEA PRIMARY KEY, -- SHA-256 hash of the activation token
	-- The user_id column is the id of the user that the token is associated with.
	-- one-to-many relationship with the users table i.e one user can have many tokens
	user_id BIGINT                      NOT NULL REFERENCES users ON DELETE CASCADE, 
	expiry  TIMESTAMP(0) WITH TIME ZONE NOT NULL, -- The expiry date of the token
	scope   TEXT                        NOT NULL 
	-- The scope of the token: 'activation',  'authentication', 'password-reset'
);
