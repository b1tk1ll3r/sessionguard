-- SessionGuard hardening for the shared Guacamole PostgreSQL database.
--
--  * makes :admin_user (the Guacamole/PocketID username of a real admin) a
--    Guacamole system administrator, creating the account if needed;
--  * deletes the initdb default account "guacadmin" (default password
--    "guacadmin"), including all of its permissions (ON DELETE CASCADE).
--
-- Existing installation:
--   docker exec -i guacamole-postgres psql -U guacamole_user -d guacamole_db \
--     -v ON_ERROR_STOP=1 -v admin_user=YOUR_POCKETID_USERNAME \
--     < production/guacamole/harden-guacamole-db.sql
--
-- New installations run this automatically via guac-init when
-- GUAC_ADMIN_USER is set (see production/sessionguard/docker-compose.yml).
--
-- The script is idempotent and aborts without changes if admin_user is
-- missing or equals guacadmin.

\set ON_ERROR_STOP on

SELECT (:'admin_user' <> '' AND lower(:'admin_user') <> 'guacadmin') AS admin_ok \gset
\if :admin_ok
\else
  \echo 'admin_user must be set to a real admin username (not guacadmin); nothing changed.'
  \quit
\endif

BEGIN;

INSERT INTO guacamole_entity (name, type)
VALUES (:'admin_user', 'USER')
ON CONFLICT (type, name) DO NOTHING;

-- The password is random and never usable: the SessionGuard extension only
-- accepts logins authenticated via the SessionGuard identity header.
INSERT INTO guacamole_user (entity_id, password_hash, password_salt, password_date)
SELECT entity_id,
       decode(md5(random()::text || clock_timestamp()::text), 'hex'),
       decode(md5(random()::text || clock_timestamp()::text), 'hex'),
       now()
FROM guacamole_entity
WHERE name = :'admin_user' AND type = 'USER'
ON CONFLICT (entity_id) DO NOTHING;

INSERT INTO guacamole_system_permission (entity_id, permission)
SELECT e.entity_id, p.permission::guacamole_system_permission_type
FROM guacamole_entity e
CROSS JOIN (VALUES ('CREATE_CONNECTION'), ('CREATE_CONNECTION_GROUP'), ('CREATE_SHARING_PROFILE'),
                   ('CREATE_USER'), ('CREATE_USER_GROUP'), ('ADMINISTER')) AS p(permission)
WHERE e.name = :'admin_user' AND e.type = 'USER'
ON CONFLICT DO NOTHING;

-- Users may edit their own account (READ/UPDATE on themselves), as initdb
-- grants it to guacadmin.
INSERT INTO guacamole_user_permission (entity_id, affected_user_id, permission)
SELECT e.entity_id, u.user_id, p.permission::guacamole_object_permission_type
FROM guacamole_entity e
JOIN guacamole_user u ON u.entity_id = e.entity_id
CROSS JOIN (VALUES ('READ'), ('UPDATE'), ('ADMINISTER')) AS p(permission)
WHERE e.name = :'admin_user' AND e.type = 'USER'
ON CONFLICT DO NOTHING;

DELETE FROM guacamole_entity WHERE name = 'guacadmin' AND type = 'USER';

COMMIT;

\echo 'Guacamole system administrators now:'
SELECT e.name AS administrator
FROM guacamole_system_permission sp
JOIN guacamole_entity e ON e.entity_id = sp.entity_id
WHERE sp.permission = 'ADMINISTER'
ORDER BY e.name;
