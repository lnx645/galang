-- Garurda Web App Database Schema
-- Jalankan: gar run examples/webapp/migrate.ga

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    email_verified_at TIMESTAMP NULL,
    remember_token VARCHAR(100) NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Posts table
CREATE TABLE IF NOT EXISTS posts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    title VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    published BOOLEAN DEFAULT FALSE,
    published_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Comments table
CREATE TABLE IF NOT EXISTS comments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    post_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Sessions table (untuk session management)
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(128) PRIMARY KEY,
    user_id INTEGER NULL,
    ip_address VARCHAR(45) NULL,
    user_agent TEXT NULL,
    payload TEXT NOT NULL,
    last_activity INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_posts_user_id ON posts(user_id);
CREATE INDEX IF NOT EXISTS idx_posts_published ON posts(published);
CREATE INDEX IF NOT EXISTS idx_posts_created_at ON posts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_comments_post_id ON comments(post_id);
CREATE INDEX IF NOT EXISTS idx_comments_user_id ON comments(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_last_activity ON sessions(last_activity);

-- Trigger untuk updated_at
CREATE TRIGGER IF NOT EXISTS trigger_users_updated_at
AFTER UPDATE ON users
BEGIN
    UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trigger_posts_updated_at
AFTER UPDATE ON posts
BEGIN
    UPDATE posts SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trigger_comments_updated_at
AFTER UPDATE ON comments
BEGIN
    UPDATE comments SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

-- Sample data
INSERT OR IGNORE INTO users (id, name, email, password_hash, created_at) VALUES
(1, 'Admin User', 'admin@example.com', 'bcrypt_hash_1', CURRENT_TIMESTAMP),
(2, 'Demo User', 'demo@example.com', 'bcrypt_hash_2', CURRENT_TIMESTAMP);

INSERT OR IGNORE INTO posts (id, user_id, title, content, published, published_at, created_at) VALUES
(1, 1, 'Selamat Datang di Garurda', '# Selamat Datang di Garurda\n\nIni adalah contoh post pertama di aplikasi Garurda Web App.', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
(2, 1, 'Fitur-fitur Garurda', '# Fitur-fitur Garurda\n\n- HTTP Server cepat\n- Blade Template Engine\n- Async/Await support\n- Database ORM\n- WebSocket and SSE', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
(3, 2, 'Contoh Post User', '# Contoh Post\n\nIni adalah post dari user demo.', 0, NULL, CURRENT_TIMESTAMP);