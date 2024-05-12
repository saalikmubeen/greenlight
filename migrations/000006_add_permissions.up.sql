CREATE TABLE IF NOT EXISTS permissions
(
	id   BIGSERIAL PRIMARY KEY,
	code TEXT NOT NULL -- "movies:read", "movies:write"
);


-- JOINING TABLE for many to many relationship
-- The relationship between permissions and users is a many-to-many relationship. 
-- One user may have many permissions, and the same permission may belong to many users.

CREATE TABLE IF NOT EXISTS users_permissions
(
	user_id       BIGINT NOT NULL REFERENCES users ON DELETE CASCADE,
	permission_id BIGINT NOT NULL REFERENCES permissions ON DELETE CASCADE,
	PRIMARY KEY (user_id, permission_id) 
	-- composite primary key, the combination of user_id and permission_id must be unique
	-- and cannot be duplicated.
);

INSERT INTO permissions (code) VALUES ('movies:read'), ('movies:write');




-- Set the activated field for alice@example.com to true.
UPDATE users SET activated = true WHERE email = 'alice@example.com';

-- Give all users the 'movies:read' permission
INSERT INTO users_permissions
SELECT id, (SELECT id FROM permissions WHERE code = 'movies:read') FROM users;

-- Give faith@example.com the 'movies:write' permission
INSERT INTO users_permissions VALUES (
    (SELECT id FROM users WHERE email = 'faith@example.com'),
    (SELECT id FROM permissions WHERE code = 'movies:write') 
);


-- List all activated users and their permissions.
SELECT email, array_agg(permissions.code) as permissions
FROM permissions
INNER JOIN users_permissions ON users_permissions.permission_id = permissions.id 
INNER JOIN users ON users_permissions.user_id = users.id
WHERE users.activated = true
GROUP BY email;



SELECT email, code FROM users
INNER JOIN users_permissions ON users.id = users_permissions.user_id
INNER JOIN permissions ON users_permissions.permission_id = permissions.id 
WHERE users.email = 'grace@example.com';