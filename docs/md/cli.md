# CLI Garurda

```bash
# Jalankan script
gar run script.ga

# REPL interaktif
gar repl

# REPL dengan preload script
gar repl script.ga

# Versi
gar version

# Bantuan
gar help
```

## Flags

`gar run` mendukung environment variable:

```bash
# Set port lewat argument langsung di script
# (http.listen(port) sudah menentukan)
```

## Systemd Service

Contoh service untuk production:

```ini
[Unit]
Description=Garurda App
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/gar run /srv/app/main.ga
WorkingDirectory=/srv/app
Restart=always
RestartSec=3
User=www-data

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable gapp.service
systemctl start gapp.service
systemctl status gapp.service
```
