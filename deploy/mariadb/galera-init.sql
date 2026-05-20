-- AetherNet Galera Cluster initialization SQL
-- Run on the bootstrap node AFTER galera_new_cluster.

-- Create AetherNet system database
CREATE DATABASE IF NOT EXISTS `aethernet`
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE `aethernet`;

-- Cluster state (tokens, RBAC) are stored in Raft FSM snapshots, not SQL.
-- MariaDB is used for: backup metadata, player data, and tenant databases.

-- Backup records
CREATE TABLE IF NOT EXISTS `backups` (
  `id`          VARCHAR(36)  NOT NULL PRIMARY KEY,
  `server_id`   VARCHAR(36)  NOT NULL,
  `object_key`  TEXT         NOT NULL,
  `size_bytes`  BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `started_at`  DATETIME     NOT NULL,
  `finished_at` DATETIME,
  `status`      ENUM('running','done','failed') NOT NULL DEFAULT 'running',
  INDEX `idx_server_id` (`server_id`),
  INDEX `idx_started_at` (`started_at`)
) ENGINE=InnoDB;

-- Player network data (sync fallback from Redis)
CREATE TABLE IF NOT EXISTS `player_profiles` (
  `uuid`         VARCHAR(36)  NOT NULL PRIMARY KEY,
  `username`     VARCHAR(16)  NOT NULL,
  `nbt_data`     LONGBLOB,
  `last_server`  VARCHAR(36),
  `updated_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX `idx_username` (`username`)
) ENGINE=InnoDB;

-- Create the AetherNet daemon service user
-- Password is set by the installer from /etc/aethernet/daemon.yaml mariadb_pass
CREATE USER IF NOT EXISTS 'aethernet'@'%' IDENTIFIED BY 'CHANGEME_ON_INSTALL';
GRANT ALL PRIVILEGES ON `aethernet`.* TO 'aethernet'@'%';
-- Grant CREATE/DROP on *.* so the provisioner can create tenant databases
GRANT CREATE, DROP, CREATE USER, GRANT OPTION ON *.* TO 'aethernet'@'%';
FLUSH PRIVILEGES;
