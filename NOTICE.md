# Notice

MEGA Downloader is distributed under the GNU General Public License, version 3 only (GPL-3.0-only), for this initial implementation.

Copyright (C) 2026 MEGA Downloader contributors.

Frontend and Go dependencies retain their own upstream copyright and license notices. The direct dependency inventory is in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), and the complete pinned transitive set is checked by `scripts/license-audit.sh` as part of the Phase H release gate.

Phase B uses the URL-safe base64 helper from github.com/t3rm1n4l/go-mega,
copyright (c) 2019 Sarath Lakshman, under the MIT License. The project-owned
resolver and transfer-facing metadata path remain implemented in
`internal/mega`; only tested protocol/account primitives are reused.
