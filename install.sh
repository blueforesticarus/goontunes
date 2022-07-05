cp -f goontunes ~/var/install/goontunes-beta/
cp .install ~/var/install/goontunes-beta/.config
ln -f bob.service ~/.config/systemd/user/
systemctl --user daemon-reload
