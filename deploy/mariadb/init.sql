-- AetherNet bootstrap schema.
-- This runs once when the MariaDB container is first started.

CREATE DATABASE IF NOT EXISTS `aethernet`
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE `aethernet`;

-- The aethernet user is created by MYSQL_USER/MYSQL_PASSWORD env vars.
-- Grant all on the aethernet DB so the daemon can provision tenant dbs.
GRANT ALL PRIVILEGES ON `aethernet`.* TO 'aethernet'@'%';
-- Allow the daemon to CREATE/DROP other databases (tenant provisioning).
GRANT CREATE, DROP ON *.* TO 'aethernet'@'%';
FLUSH PRIVILEGES;

-- Placeholder tables — the daemon creates real schema via Raft FSM state.
CREATE TABLE IF NOT EXISTS _meta (
  `key`   VARCHAR(128) NOT NULL PRIMARY KEY,
  `value` TEXT         NOT NULL,
  `ts`    BIGINT       NOT NULL DEFAULT 0
) ENGINE=InnoDB;
