package main

import (
	"database/sql"
	"log"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	dbURL := os.Getenv("DATABASE_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("❌ Error conectando a PostgreSQL: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("❌ Error haciendo ping a PostgreSQL: %v", err)
	}

	log.Println("🔄 Iniciando migración de esquema...")

	schema := `
	-- 1. Tablas de Usuarios y Autenticación
	CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		email VARCHAR(255) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		name VARCHAR(100) NOT NULL,
		username VARCHAR(50) UNIQUE NOT NULL,
		role VARCHAR(20) NOT NULL DEFAULT 'USER',
		avatar_url TEXT,
		is_kyc_verified BOOLEAN DEFAULT false,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS vibe_profiles (
		user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		favorite_genres TEXT[],
		departure_zone VARCHAR(100),
		party_style VARCHAR(50),
		verified_kyc_only BOOLEAN DEFAULT false,
		spotify_connected BOOLEAN DEFAULT false
	);

	CREATE TABLE IF NOT EXISTS kyc_sessions (
		id VARCHAR(100) PRIMARY KEY,
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		status VARCHAR(30) NOT NULL DEFAULT 'CREATED',
		document_type VARCHAR(50) DEFAULT 'ID_CARD',
		document_country VARCHAR(3) DEFAULT 'URY',
		doc_front_url TEXT,
		doc_back_url TEXT,
		face_url TEXT,
		match_score DECIMAL(5,2),
		extracted_data JSONB,
		failure_reason TEXT,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	);

	-- 2. Tablas de Eventos y Entradas
	CREATE TABLE IF NOT EXISTS events (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		title VARCHAR(150) NOT NULL,
		producer_id UUID REFERENCES users(id),
		date TIMESTAMP NOT NULL,
		location VARCHAR(255) NOT NULL,
		cinematic_banner_url TEXT,
		description TEXT,
		lineup TEXT[],
		is_featured BOOLEAN DEFAULT false,
		created_at TIMESTAMP DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS event_rsvps (
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		event_id UUID REFERENCES events(id) ON DELETE CASCADE,
		status VARCHAR(20),
		created_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (user_id, event_id)
	);

	CREATE TABLE IF NOT EXISTS tickets (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		event_id UUID REFERENCES events(id) ON DELETE CASCADE,
		seller_id UUID REFERENCES users(id),
		buyer_id UUID REFERENCES users(id),
		price DECIMAL(10,2) NOT NULL,
		status VARCHAR(30) NOT NULL DEFAULT 'AVAILABLE',
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	);

	-- 3. Comunidades y Feed
	CREATE TABLE IF NOT EXISTS communities (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name VARCHAR(100) NOT NULL,
		pr_owner_id UUID REFERENCES users(id),
		cover_image_url TEXT,
		description TEXT,
		created_at TIMESTAMP DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS community_members (
		community_id UUID REFERENCES communities(id) ON DELETE CASCADE,
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		joined_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (community_id, user_id)
	);

	CREATE TABLE IF NOT EXISTS posts (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		author_id UUID REFERENCES users(id) ON DELETE CASCADE,
		event_id UUID REFERENCES events(id) ON DELETE CASCADE,
		community_id UUID REFERENCES communities(id) ON DELETE CASCADE,
		title VARCHAR(255),
		content TEXT NOT NULL,
		long_content TEXT,
		header_image_url TEXT,
		timestamp TIMESTAMP DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS post_likes (
		post_id UUID REFERENCES posts(id) ON DELETE CASCADE,
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (post_id, user_id)
	);

	CREATE TABLE IF NOT EXISTS comments (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		target_type VARCHAR(20) NOT NULL,
		target_id UUID NOT NULL,
		author_id UUID REFERENCES users(id) ON DELETE CASCADE,
		content TEXT NOT NULL,
		timestamp TIMESTAMP DEFAULT NOW()
	);

	-- 4. Crews / Squads
	CREATE TABLE IF NOT EXISTS squads (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		event_id UUID REFERENCES events(id) ON DELETE CASCADE,
		name VARCHAR(100) NOT NULL,
		departure_zone VARCHAR(100),
		chat_room_id UUID,
		status VARCHAR(30) NOT NULL DEFAULT 'forming',
		created_at TIMESTAMP DEFAULT NOW(),
		expires_at TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS squad_members (
		squad_id UUID REFERENCES squads(id) ON DELETE CASCADE,
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		role VARCHAR(20) DEFAULT 'member',
		has_ticket BOOLEAN DEFAULT false,
		joined_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (squad_id, user_id)
	);

	-- 5. Chat
	CREATE TABLE IF NOT EXISTS chats (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		type VARCHAR(20) NOT NULL,
		last_message TEXT,
		updated_at TIMESTAMP DEFAULT NOW(),
		created_at TIMESTAMP DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS chat_participants (
		chat_id UUID REFERENCES chats(id) ON DELETE CASCADE,
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		joined_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (chat_id, user_id)
	);

	CREATE TABLE IF NOT EXISTS messages (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		chat_id UUID REFERENCES chats(id) ON DELETE CASCADE,
		sender_id UUID REFERENCES users(id),
		content TEXT NOT NULL,
		type VARCHAR(30) NOT NULL DEFAULT 'text',
		metadata JSONB,
		timestamp TIMESTAMP DEFAULT NOW()
	);

	-- Índices
	CREATE INDEX IF NOT EXISTS idx_events_date ON events(date);
	CREATE INDEX IF NOT EXISTS idx_posts_event_id ON posts(event_id);
	CREATE INDEX IF NOT EXISTS idx_posts_community_id ON posts(community_id);
	CREATE INDEX IF NOT EXISTS idx_comments_target ON comments(target_type, target_id);
	CREATE INDEX IF NOT EXISTS idx_messages_chat_id_timestamp ON messages(chat_id, timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_squads_event_id ON squads(event_id);
	CREATE INDEX IF NOT EXISTS idx_tickets_event_status ON tickets(event_id, status);
	`

	_, err = db.Exec(schema)
	if err != nil {
		log.Fatalf("❌ Error ejecutando esquema: %v", err)
	}
	log.Println("✅ Esquema creado con éxito.")

	log.Println("🔄 Insertando datos de ejemplo (Seed)...")

	seed := `
	-- Insertar Usuarios de Ejemplo (Ignorar si existen)
	INSERT INTO users (id, email, password_hash, name, username, role, is_kyc_verified)
	VALUES 
	('11111111-1111-1111-1111-111111111111', 'juan@example.com', 'hash', 'Juan Perez', 'juanp', 'USER', true),
	('22222222-2222-2222-2222-222222222222', 'rrpp@example.com', 'hash', 'Fiesta VIP RRPP', 'fiestavip', 'RRPP', true)
	ON CONFLICT (email) DO NOTHING;

	-- Insertar Evento de Ejemplo
	INSERT INTO events (id, title, producer_id, date, location, description, is_featured)
	VALUES 
	('33333333-3333-3333-3333-333333333333', 'Keylife Punta del Este', '22222222-2222-2222-2222-222222222222', NOW() + INTERVAL '10 days', 'Punta del Este, UY', 'Festival de música electrónica', true)
	ON CONFLICT DO NOTHING;

	-- Insertar Vibe Profile
	INSERT INTO vibe_profiles (user_id, favorite_genres, departure_zone, party_style, verified_kyc_only)
	VALUES 
	('11111111-1111-1111-1111-111111111111', ARRAY['Techno', 'House'], 'Montevideo', 'full_night', true)
	ON CONFLICT DO NOTHING;
	`

	_, err = db.Exec(seed)
	if err != nil {
		log.Printf("⚠️ Error o conflicto insertando seed: %v", err)
	} else {
		log.Println("✅ Datos de ejemplo insertados.")
	}
}
