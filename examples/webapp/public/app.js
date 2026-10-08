// Contoh web Garurda — JavaScript polos, tanpa library.
// (CSS menangani menu burger; fokus JS di sini: interaksi API + jam.)

(function () {
    "use strict";

    // ── Tandai menu aktif sesuai path halaman ───────────────
    var path = window.location.pathname;
    document.querySelectorAll(".menu a").forEach(function (a) {
        var href = a.getAttribute("href");
        if (href === path || (href !== "/" && path.indexOf(href) === 0)) {
            a.classList.add("active");
        }
    });

    // ── Util ────────────────────────────────────────────────
    function show(el, data, isError) {
        el.textContent = typeof data === "string"
            ? data
            : JSON.stringify(data, null, 2);
        el.classList.toggle("error", !!isError);
    }

    function hit(url, opts) {
        return fetch(url, opts).then(function (res) {
            return res.json().then(function (body) {
                return { status: res.status, body: body };
            }).catch(function () {
                return { status: res.status, body: "(bukan JSON)" };
            });
        });
    }

    // ── GET /api/sapa/{nama} ────────────────────────────────
    var formSapa = document.getElementById("form-sapa");
    if (formSapa) {
        var hasilSapa = document.getElementById("hasil-sapa");
        formSapa.addEventListener("submit", function (e) {
            e.preventDefault();
            var nama = document.getElementById("inp-nama").value.trim() || "dunia";
            show(hasilSapa, "// GET /api/sapa/" + nama + " …");
            hit("/api/sapa/" + encodeURIComponent(nama)).then(function (r) {
                show(hasilSapa, r.body, r.status >= 400);
            }).catch(function (err) {
                show(hasilSapa, "gagal: " + err, true);
            });
        });
    }

    // ── POST /api/echo + tombol 404 ─────────────────────────
    var formEcho = document.getElementById("form-echo");
    if (formEcho) {
        var hasilEcho = document.getElementById("hasil-echo");
        formEcho.addEventListener("submit", function (e) {
            e.preventDefault();
            var teks = document.getElementById("inp-echo").value;
            var objek;
            try {
                objek = JSON.parse(teks);
            } catch (err) {
                show(hasilEcho, "JSON tidak valid: " + err, true);
                return;
            }
            show(hasilEcho, "// POST /api/echo …");
            hit("/api/echo", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(objek)
            }).then(function (r) {
                show(hasilEcho, r.body, r.status >= 400);
            }).catch(function (err) {
                show(hasilEcho, "gagal: " + err, true);
            });
        });

        var btnUser = document.getElementById("btn-user");
        if (btnUser) {
            btnUser.addEventListener("click", function () {
                show(hasilEcho, "// GET /api/user/999 …");
                hit("/api/user/999").then(function (r) {
                    show(hasilEcho, r.body, r.status >= 400);
                }).catch(function (err) {
                    show(hasilEcho, "gagal: " + err, true);
                });
            });
        }
    }

    // ── GET /api/kunjungan + reset sesi ─────────────────────
    var btnKunjungan = document.getElementById("btn-kunjungan");
    var hasilKunjungan = document.getElementById("hasil-kunjungan");
    if (btnKunjungan && hasilKunjungan) {
        var hitung = function (tambahan) {
            show(hasilKunjungan, "// GET /api/kunjungan" + tambahan + " …");
            hit("/api/kunjungan" + tambahan).then(function (r) {
                show(hasilKunjungan, r.body, r.status >= 400);
            }).catch(function (err) {
                show(hasilKunjungan, "gagal: " + err, true);
            });
        };
        btnKunjungan.addEventListener("click", function () { hitung(""); });
        var btnResetSesi = document.getElementById("btn-reset-sesi");
        if (btnResetSesi) {
            btnResetSesi.addEventListener("click", function () { hitung("?reset=1"); });
        }
    }

    // ── Jam lokal ───────────────────────────────────────────
    var jam = document.getElementById("jam");
    if (jam) {
        var tik = function () {
            jam.textContent = new Date().toLocaleString("id-ID", {
                weekday: "long", year: "numeric", month: "long",
                day: "numeric", hour: "2-digit", minute: "2-digit",
                second: "2-digit"
            });
        };
        tik();
        setInterval(tik, 1000);
    }
})();
