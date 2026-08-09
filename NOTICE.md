# Notice

MEGA Downloader is distributed under the GNU General Public License, version 3 only (GPL-3.0-only), for this initial implementation.

Copyright (C) 2026 MEGA Downloader contributors.

Frontend and Go dependencies retain their own upstream copyright and license notices. A complete dependency/license audit is part of the Phase H release gate.

Phase B uses the URL-safe base64 helper from github.com/t3rm1n4l/go-mega,
copyright (c) 2019 Sarath Lakshman, under the MIT License. Its public-link
APIs are not used; the project-owned resolver and transfer-facing metadata
path are implemented in internal/mega.
