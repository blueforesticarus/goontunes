cp goontunes .config ~/var/install/goontunes-beta/
ln bob.service ~/.config/systemd/user/
systemctl --user daemon-reload
