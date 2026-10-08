package interp

// Sesi HTTP: objek per sesi yang disimpan di memori dan diikat ke cookie
// HttpOnly. Penyimpanan hidup selama proses — restart server menghapus
// semua sesi, jadi modul ini cocok untuk demo/pengembangan, bukan untuk
// klaster.

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"garurda/internal/domain"
)

// sessionCookieName adalah cookie pembawa id sesi.
const sessionCookieName = "garurda_session"

// sessionStore menyimpan objek sesi dalam memori, dikunci id sesi.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]*domain.Obj
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: make(map[string]*domain.Obj)}
}

// get mengembalikan objek sesi untuk sid, atau nil bila tidak dikenal.
func (ss *sessionStore) get(sid string) *domain.Obj {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.m[sid]
}

// put menyimpan objek di bawah id acak 128-bit dan mengembalikan id itu.
func (ss *sessionStore) put(obj *domain.Obj) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	sid := hex.EncodeToString(b[:])
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.m[sid] = obj
	return sid, nil
}

// delete menghapus sid dari store.
func (ss *sessionStore) delete(sid string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.m, sid)
}

// sessionOf mengambil pasangan (objek sesi, id) dari objek request yang
// dibuat buildRequestObj.
func sessionOf(req *domain.Obj) (*domain.Obj, bool) {
	v, has := req.Get("session")
	if !has {
		return nil, false
	}
	sess, ok := v.(*domain.Obj)
	return sess, ok
}

// finalizeSession berjalan setelah handler: sesi yang ditulis tetapi
// belum ber-id disimpan dan nilainya menjadi header Set-Cookie. Sesi
// yang tak tersentuh (objek kosong) tidak pernah masuk store, jadi
// pengunjung anonim tidak menumpuk entri. Sesi yang baru dihancurkan
// menghasilkan cookie kedaluwarsa.
func (in *Interp) finalizeSession(req *domain.Obj) (string, error) {
	sess, ok := sessionOf(req)
	if !ok {
		return "", nil
	}
	// Sudah ber-id: mutasi langsung hidup di objek yang sama di store.
	if sidV, has := req.Get("session_id"); has {
		if _, isNull := sidV.(domain.Null); !isNull {
			return "", nil
		}
	}
	if len(sess.Keys()) > 0 {
		sid, err := in.sessions.put(sess)
		if err != nil {
			return "", err
		}
		req.Set("session_id", domain.Str(sid))
		return sessionCookieName + "=" + sid + "; Path=/; HttpOnly; SameSite=Lax", nil
	}
	if _, expired := req.Get("_session_expired"); expired {
		return sessionCookieName + "=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0", nil
	}
	return "", nil
}
