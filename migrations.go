package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type Migration struct {
	ID      int
	Name    string
	UpSQL   string
	DownSQL string
}

var migrations = []Migration{
	{
		ID:    1,
		Name:  "initial_schema",
		UpSQL: initialSchemaSQL,
	},
	{
		ID:   2,
		Name: "add_proxy_url",
		UpSQL: `
            -- PostgreSQL version
            DO $$
            BEGIN
                IF NOT EXISTS (
                    SELECT 1 FROM information_schema.columns 
                    WHERE table_name = 'users' AND column_name = 'proxy_url'
                ) THEN
                    ALTER TABLE users ADD COLUMN proxy_url TEXT DEFAULT '';
                END IF;
            END $$;
            
            -- SQLite version (handled in code)
            `,
	},
	{
		ID:    3,
		Name:  "change_id_to_string",
		UpSQL: changeIDToStringSQL,
	},
	{
		ID:    4,
		Name:  "add_s3_support",
		UpSQL: addS3SupportSQL,
	},
	{
		ID:    5,
		Name:  "user_webhooks",
		UpSQL: addUserWebhooksSQL,
	},
	{
		ID:    6,
		Name:  "add_chatwoot_support",
		UpSQL: addChatwootSupportSQL,
	},
	{
		ID:    7,
		Name:  "add_chatwoot_groups_support",
		UpSQL: addChatwootGroupsSupportSQL,
	},
	{
		ID:    8,
		Name:  "remove_chatwoot_process_group_participants",
		UpSQL: removeChatwootProcessGroupParticipantsSQL,
	},
	{
		ID:    9,
		Name:  "remove_chatwoot_group_mention_only",
		UpSQL: removeChatwootGroupMentionOnlySQL,
	},
	{
		ID:    10,
		Name:  "add_chatwoot_typing_indicator",
		UpSQL: addChatwootTypingIndicatorSQL,
	},
	{
		ID:    11,
		Name:  "create_messages_table",
		UpSQL: createMessagesTableSQL,
	},
	{
		ID:    12,
		Name:  "add_messages_updated_at",
		UpSQL: addMessagesUpdatedAtSQL,
	},
	{
		ID:    13,
		Name:  "add_chatwoot_conversation_id",
		UpSQL: addChatwootConversationIdSQL,
	},
}

const changeIDToStringSQL = `
-- Migration to change ID from integer to random string
DO $$
BEGIN
    -- Only execute if the column is currently integer type
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'users' AND column_name = 'id' AND data_type = 'integer'
    ) THEN
        -- For PostgreSQL
        ALTER TABLE users ADD COLUMN new_id TEXT;
		UPDATE users SET new_id = md5(random()::text || id::text || clock_timestamp()::text);
		ALTER TABLE users DROP CONSTRAINT users_pkey;
        ALTER TABLE users DROP COLUMN id CASCADE;
        ALTER TABLE users RENAME COLUMN new_id TO id;
        ALTER TABLE users ALTER COLUMN id SET NOT NULL;
        ALTER TABLE users ADD PRIMARY KEY (id);
    END IF;
END $$;
`

const initialSchemaSQL = `
-- PostgreSQL version
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'users') THEN
        CREATE TABLE users (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            token TEXT NOT NULL,
            jid TEXT NOT NULL DEFAULT '',
            qrcode TEXT NOT NULL DEFAULT '',
            connected INTEGER,
            expiration INTEGER,
            proxy_url TEXT DEFAULT ''
        );
    END IF;
END $$;

-- SQLite version (handled in code)
`

const addS3SupportSQL = `
-- PostgreSQL version
DO $$
BEGIN
    -- Add S3 configuration columns if they don't exist
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_enabled') THEN
        ALTER TABLE users ADD COLUMN s3_enabled BOOLEAN DEFAULT FALSE;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_endpoint') THEN
        ALTER TABLE users ADD COLUMN s3_endpoint TEXT DEFAULT '';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_region') THEN
        ALTER TABLE users ADD COLUMN s3_region TEXT DEFAULT '';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_bucket') THEN
        ALTER TABLE users ADD COLUMN s3_bucket TEXT DEFAULT '';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_access_key') THEN
        ALTER TABLE users ADD COLUMN s3_access_key TEXT DEFAULT '';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_secret_key') THEN
        ALTER TABLE users ADD COLUMN s3_secret_key TEXT DEFAULT '';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_path_style') THEN
        ALTER TABLE users ADD COLUMN s3_path_style BOOLEAN DEFAULT TRUE;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_public_url') THEN
        ALTER TABLE users ADD COLUMN s3_public_url TEXT DEFAULT '';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'media_delivery') THEN
        ALTER TABLE users ADD COLUMN media_delivery TEXT DEFAULT 'base64';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 's3_retention_days') THEN
        ALTER TABLE users ADD COLUMN s3_retention_days INTEGER DEFAULT 30;
    END IF;
END $$;
`

const addUserWebhooksSQL = `
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'user_webhooks') THEN
        CREATE TABLE user_webhooks (
            id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            url TEXT NOT NULL,
            events TEXT NOT NULL DEFAULT ''
        );
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'webhook') THEN
        ALTER TABLE users DROP COLUMN webhook;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'events') THEN
        ALTER TABLE users DROP COLUMN events;
    END IF;
END $$;
`

// GenerateRandomID creates a random string ID
func GenerateRandomID() (string, error) {
	bytes := make([]byte, 16) // 128 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// Initialize the database with migrations
func initializeSchema(db *sqlx.DB) error {
	// Create migrations table if it doesn't exist
	if err := createMigrationsTable(db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get already applied migrations
	applied, err := getAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Apply missing migrations
	for _, migration := range migrations {
		if _, ok := applied[migration.ID]; !ok {
			if err := applyMigration(db, migration); err != nil {
				return fmt.Errorf("failed to apply migration %d: %w", migration.ID, err)
			}
		}
	}

	return nil
}

func createMigrationsTable(db *sqlx.DB) error {
	var tableExists bool
	var err error

	switch db.DriverName() {
	case "postgres":
		err = db.Get(&tableExists, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables 
				WHERE table_name = 'migrations'
			)`)
	case "sqlite":
		err = db.Get(&tableExists, `
			SELECT EXISTS (
				SELECT 1 FROM sqlite_master 
				WHERE type='table' AND name='migrations'
			)`)
	default:
		return fmt.Errorf("unsupported database driver: %s", db.DriverName())
	}

	if err != nil {
		return fmt.Errorf("failed to check migrations table existence: %w", err)
	}

	if tableExists {
		return nil
	}

	_, err = db.Exec(`
		CREATE TABLE migrations (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	return nil
}

func getAppliedMigrations(db *sqlx.DB) (map[int]struct{}, error) {
	applied := make(map[int]struct{})
	var rows []struct {
		ID   int    `db:"id"`
		Name string `db:"name"`
	}

	err := db.Select(&rows, "SELECT id, name FROM migrations ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("failed to query applied migrations: %w", err)
	}

	for _, row := range rows {
		applied[row.ID] = struct{}{}
	}

	return applied, nil
}

func applyMigration(db *sqlx.DB, migration Migration) error {
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if migration.ID == 1 {
		// Handle initial schema creation differently per database
		if db.DriverName() == "sqlite" {
			err = createTableIfNotExistsSQLite(tx, "users", `
                CREATE TABLE users (
                    id TEXT PRIMARY KEY,
                    name TEXT NOT NULL,
                    token TEXT NOT NULL,
                    jid TEXT NOT NULL DEFAULT '',
                    qrcode TEXT NOT NULL DEFAULT '',
                    connected INTEGER,
                    expiration INTEGER,
                    proxy_url TEXT DEFAULT ''
                )`)
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else if migration.ID == 2 {
		if db.DriverName() == "sqlite" {
			err = addColumnIfNotExistsSQLite(tx, "users", "proxy_url", "TEXT DEFAULT ''")
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else if migration.ID == 3 {
		if db.DriverName() == "sqlite" {
			err = migrateSQLiteIDToString(tx)
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else if migration.ID == 4 {
		if db.DriverName() == "sqlite" {
			// Handle S3 columns for SQLite
			err = addColumnIfNotExistsSQLite(tx, "users", "s3_enabled", "BOOLEAN DEFAULT 0")
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_endpoint", "TEXT DEFAULT ''")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_region", "TEXT DEFAULT ''")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_bucket", "TEXT DEFAULT ''")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_access_key", "TEXT DEFAULT ''")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_secret_key", "TEXT DEFAULT ''")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_path_style", "BOOLEAN DEFAULT 1")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_public_url", "TEXT DEFAULT ''")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "media_delivery", "TEXT DEFAULT 'base64'")
			}
			if err == nil {
				err = addColumnIfNotExistsSQLite(tx, "users", "s3_retention_days", "INTEGER DEFAULT 30")
			}
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else if migration.ID == 5 {
		if db.DriverName() == "sqlite" {
			err = createTableIfNotExistsSQLite(tx, "user_webhooks", `
                CREATE TABLE user_webhooks (
                    id TEXT PRIMARY KEY,
                    user_id TEXT NOT NULL,
                    url TEXT NOT NULL,
                    events TEXT NOT NULL DEFAULT ''
                )`)
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else if migration.ID == 6 {
		if db.DriverName() == "sqlite" {
			err = createTableIfNotExistsSQLite(tx, "chatwoot_configs", `
                CREATE TABLE chatwoot_configs (
                    id TEXT PRIMARY KEY,
                    user_id TEXT NOT NULL,
                    enabled BOOLEAN DEFAULT 0,
                    account_id TEXT NOT NULL,
                    token TEXT NOT NULL,
                    url TEXT NOT NULL,
                    name_inbox TEXT DEFAULT '',
                    sign_msg BOOLEAN DEFAULT 0,
                    sign_delimiter TEXT DEFAULT '\n',
                    reopen_conversation BOOLEAN DEFAULT 1,
                    conversation_pending BOOLEAN DEFAULT 0,
                    merge_brazil_contacts BOOLEAN DEFAULT 0,
                    ignore_jids TEXT DEFAULT '[]',
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )`)
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else if migration.ID == 11 {
		if db.DriverName() == "sqlite" {
			err = createTableIfNotExistsSQLite(tx, "messages", `
                CREATE TABLE messages (
                    id TEXT PRIMARY KEY,              -- WhatsApp message ID (evt.Info.ID)
                    user_id TEXT NOT NULL,           -- Referência ao usuário
                    content TEXT DEFAULT '',         -- Conteúdo da mensagem
                    sender_name TEXT DEFAULT '',     -- Nome do remetente
                    message_type TEXT DEFAULT 'text', -- "text", "image", "video", "audio", "document"
                    chatwoot_message_id INTEGER,     -- ID da mensagem no Chatwoot (NULL até ser enviada)
                    from_me BOOLEAN DEFAULT 0,       -- evt.Info.IsFromMe (SQLite usa 0/1 para boolean)
                    chat_jid TEXT NOT NULL,          -- evt.Info.Chat para lookup
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )`)
			// Adicionar índices SQLite
			if err == nil {
				_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_messages_user_id ON messages(user_id)")
			}
			if err == nil {
				_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_messages_chatwoot_id ON messages(chatwoot_message_id)")
			}
			if err == nil {
				_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_messages_chat_jid ON messages(chat_jid)")
			}
			if err == nil {
				_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)")
			}
		} else {
			_, err = tx.Exec(migration.UpSQL)
		}
	} else {
		_, err = tx.Exec(migration.UpSQL)
	}

	if err != nil {
		return fmt.Errorf("failed to execute migration SQL: %w", err)
	}

	// Record the migration
	if _, err = tx.Exec(`
        INSERT INTO migrations (id, name) 
        VALUES ($1, $2)`, migration.ID, migration.Name); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

func createTableIfNotExistsSQLite(tx *sqlx.Tx, tableName, createSQL string) error {
	var exists int
	err := tx.Get(&exists, `
        SELECT COUNT(*) FROM sqlite_master
        WHERE type='table' AND name=?`, tableName)
	if err != nil {
		return err
	}

	if exists == 0 {
		_, err = tx.Exec(createSQL)
		return err
	}
	return nil
}
func sqliteChangeIDType(tx *sqlx.Tx) error {
	// SQLite requires a more complex approach:
	// 1. Create new table with string ID
	// 2. Copy data with new UUIDs
	// 3. Drop old table
	// 4. Rename new table

	// Step 1: Get the current schema
	var tableInfo string
	err := tx.Get(&tableInfo, `
        SELECT sql FROM sqlite_master
        WHERE type='table' AND name='users'`)
	if err != nil {
		return fmt.Errorf("failed to get table info: %w", err)
	}

	// Step 2: Create new table with string ID
	newTableSQL := strings.Replace(tableInfo,
		"CREATE TABLE users (",
		"CREATE TABLE users_new (id TEXT PRIMARY KEY, ", 1)
	newTableSQL = strings.Replace(newTableSQL,
		"id INTEGER PRIMARY KEY AUTOINCREMENT,", "", 1)

	if _, err = tx.Exec(newTableSQL); err != nil {
		return fmt.Errorf("failed to create new table: %w", err)
	}

	// Step 3: Copy data with new UUIDs
	columns, err := getTableColumns(tx, "users")
	if err != nil {
		return fmt.Errorf("failed to get table columns: %w", err)
	}

	// Remove 'id' from columns list
	var filteredColumns []string
	for _, col := range columns {
		if col != "id" {
			filteredColumns = append(filteredColumns, col)
		}
	}

	columnList := strings.Join(filteredColumns, ", ")
	if _, err = tx.Exec(fmt.Sprintf(`
        INSERT INTO users_new (id, %s)
        SELECT gen_random_uuid(), %s FROM users`,
		columnList, columnList)); err != nil {
		return fmt.Errorf("failed to copy data: %w", err)
	}

	// Step 4: Drop old table
	if _, err = tx.Exec("DROP TABLE users"); err != nil {
		return fmt.Errorf("failed to drop old table: %w", err)
	}

	// Step 5: Rename new table
	if _, err = tx.Exec("ALTER TABLE users_new RENAME TO users"); err != nil {
		return fmt.Errorf("failed to rename table: %w", err)
	}

	return nil
}

func getTableColumns(tx *sqlx.Tx, tableName string) ([]string, error) {
	var columns []string
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dfltValue interface{}
		var pk int

		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("failed to scan column info: %w", err)
		}
		columns = append(columns, name)
	}

	return columns, nil
}

func migrateSQLiteIDToString(tx *sqlx.Tx) error {
	// 1. Check if we need to do the migration
	var currentType string
	err := tx.QueryRow(`
        SELECT type FROM pragma_table_info('users')
        WHERE name = 'id'`).Scan(&currentType)
	if err != nil {
		return fmt.Errorf("failed to check column type: %w", err)
	}

	if currentType != "INTEGER" {
		// No migration needed
		return nil
	}

	// 2. Create new table with string ID
	_, err = tx.Exec(`
        CREATE TABLE users_new (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            token TEXT NOT NULL,
            webhook TEXT NOT NULL DEFAULT '',
            jid TEXT NOT NULL DEFAULT '',
            qrcode TEXT NOT NULL DEFAULT '',
            connected INTEGER,
            expiration INTEGER,
            events TEXT NOT NULL DEFAULT '',
            proxy_url TEXT DEFAULT ''
        )`)
	if err != nil {
		return fmt.Errorf("failed to create new table: %w", err)
	}

	// 3. Copy data with new UUIDs
	_, err = tx.Exec(`
        INSERT INTO users_new
        SELECT
            hex(randomblob(16)),
            name, token, webhook, jid, qrcode,
            connected, expiration, events, proxy_url 
        FROM users`)
	if err != nil {
		return fmt.Errorf("failed to copy data: %w", err)
	}

	// 4. Drop old table
	_, err = tx.Exec(`DROP TABLE users`)
	if err != nil {
		return fmt.Errorf("failed to drop old table: %w", err)
	}

	// 5. Rename new table
	_, err = tx.Exec(`ALTER TABLE users_new RENAME TO users`)
	if err != nil {
		return fmt.Errorf("failed to rename table: %w", err)
	}

	return nil
}

func addColumnIfNotExistsSQLite(tx *sqlx.Tx, tableName, columnName, columnDef string) error {
	var exists int
	err := tx.Get(&exists, `
        SELECT COUNT(*) FROM pragma_table_info(?)
        WHERE name = ?`, tableName, columnName)
	if err != nil {
		return fmt.Errorf("failed to check column existence: %w", err)
	}

	if exists == 0 {
		_, err = tx.Exec(fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s %s",
			tableName, columnName, columnDef))
		if err != nil {
			return fmt.Errorf("failed to add column: %w", err)
		}
	}
	return nil
}

const addChatwootSupportSQL = `
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'chatwoot_configs') THEN
        CREATE TABLE chatwoot_configs (
            id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            enabled BOOLEAN DEFAULT FALSE,
            account_id TEXT NOT NULL,
            token TEXT NOT NULL,
            url TEXT NOT NULL,
            name_inbox TEXT DEFAULT '',
            sign_msg BOOLEAN DEFAULT FALSE,
            sign_delimiter TEXT DEFAULT '\n',
            reopen_conversation BOOLEAN DEFAULT TRUE,
            conversation_pending BOOLEAN DEFAULT FALSE,
            merge_brazil_contacts BOOLEAN DEFAULT FALSE,
            ignore_jids TEXT DEFAULT '[]',
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
    END IF;
END $$;
`

const addChatwootGroupsSupportSQL = `
DO $$
BEGIN
    -- Adicionar campo ignore_groups se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'chatwoot_configs' AND column_name = 'ignore_groups'
    ) THEN
        ALTER TABLE chatwoot_configs ADD COLUMN ignore_groups BOOLEAN DEFAULT TRUE;
    END IF;
    
    -- Adicionar campo process_group_participants se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'chatwoot_configs' AND column_name = 'process_group_participants'
    ) THEN
        ALTER TABLE chatwoot_configs ADD COLUMN process_group_participants BOOLEAN DEFAULT TRUE;
    END IF;
    
    -- Adicionar campo group_mention_only se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'chatwoot_configs' AND column_name = 'group_mention_only'
    ) THEN
        ALTER TABLE chatwoot_configs ADD COLUMN group_mention_only BOOLEAN DEFAULT FALSE;
    END IF;
END $$;
`

const removeChatwootProcessGroupParticipantsSQL = `
DO $$
BEGIN
    -- Remover campo process_group_participants se existir
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'chatwoot_configs' AND column_name = 'process_group_participants'
    ) THEN
        ALTER TABLE chatwoot_configs DROP COLUMN process_group_participants;
    END IF;
END $$;
`

const removeChatwootGroupMentionOnlySQL = `
DO $$
BEGIN
    -- Remover campo group_mention_only se existir
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'chatwoot_configs' AND column_name = 'group_mention_only'
    ) THEN
        ALTER TABLE chatwoot_configs DROP COLUMN group_mention_only;
    END IF;
END $$;
`

const addChatwootTypingIndicatorSQL = `
DO $$
BEGIN
    -- Adicionar campo enable_typing_indicator se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'chatwoot_configs' AND column_name = 'enable_typing_indicator'
    ) THEN
        ALTER TABLE chatwoot_configs ADD COLUMN enable_typing_indicator BOOLEAN DEFAULT FALSE;
    END IF;
END $$;
`

const createMessagesTableSQL = `
-- Criar tabela messages para histórico e vinculação WhatsApp ↔ Chatwoot
-- PostgreSQL version
DO $$
BEGIN
    -- Criar tabela se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.tables 
        WHERE table_name = 'messages'
    ) THEN
        CREATE TABLE messages (
            id TEXT PRIMARY KEY,              -- WhatsApp message ID (evt.Info.ID)
            user_id TEXT NOT NULL,           -- Referência ao usuário
            content TEXT DEFAULT '',         -- Conteúdo da mensagem
            sender_name TEXT DEFAULT '',     -- Nome do remetente
            message_type TEXT DEFAULT 'text', -- "text", "image", "video", "audio", "document"
            chatwoot_message_id INTEGER,     -- ID da mensagem no Chatwoot (NULL até ser enviada)
            from_me BOOLEAN DEFAULT FALSE,   -- evt.Info.IsFromMe
            chat_jid TEXT NOT NULL,          -- evt.Info.Chat para lookup
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        );
        
        -- Índices para performance
        CREATE INDEX idx_messages_user_id ON messages(user_id);
        CREATE INDEX idx_messages_chatwoot_id ON messages(chatwoot_message_id);
        CREATE INDEX idx_messages_chat_jid ON messages(chat_jid);
        CREATE INDEX idx_messages_created_at ON messages(created_at);
        
    END IF;
END $$;

-- SQLite version (fallback)
-- Esta versão será executada via código se for SQLite
`

const addMessagesUpdatedAtSQL = `
-- Adicionar coluna updated_at à tabela messages para controle anti-loop
-- PostgreSQL version
DO $$
BEGIN
    -- Adicionar coluna se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'messages' AND column_name = 'updated_at'
    ) THEN
        ALTER TABLE messages ADD COLUMN updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;
        
        -- Criar índice para performance
        CREATE INDEX idx_messages_updated_at ON messages(updated_at);
        
        -- Inicializar updated_at com valor de created_at para registros existentes
        UPDATE messages SET updated_at = created_at WHERE updated_at IS NULL;
        
    END IF;
END $$;

-- SQLite version (fallback)
-- Esta versão será executada via código se for SQLite
`

const addChatwootConversationIdSQL = `
-- Adicionar coluna chatwoot_conversation_id à tabela messages para deleção bidirecional
-- PostgreSQL version
DO $$
BEGIN
    -- Adicionar coluna se não existir
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'messages' AND column_name = 'chatwoot_conversation_id'
    ) THEN
        ALTER TABLE messages ADD COLUMN chatwoot_conversation_id INTEGER;
        
        -- Criar índice para performance
        CREATE INDEX idx_messages_chatwoot_conv_id ON messages(chatwoot_conversation_id);
        
    END IF;
END $$;

-- SQLite version (fallback)
-- Esta versão será executada via código se for SQLite
`
