# 🍪 YouTube Cookies Guide

This system allows `yt-dlp` to authenticate with YouTube, bypassing age restrictions, bot challenges ("Sign in to confirm you're not a bot"), region locks, and rate limits.

---

## 📁 Cookie Locations & Priority

The application checks for a valid Netscape cookie file in the following order:

1. **Configured Environment File:** `YOUTUBE_COOKIES_FILE` (if set)
2. **Default Local Location:** `config/cookie/cookie.txt`
3. **Legacy Local Directory:** `config/cookies/cookie.txt` or `config/cookies/cookies.txt`
4. **Any `.txt` in `config/cookies/`:** Any Netscape-formatted `.txt` file (e.g. downloaded from `COOKIES_LINK`)

If no cookie file is present or readable, the application **automatically and gracefully continues with unauthenticated playback** without interrupting service or crashing.

---

## 💻 Local Setup (Development)

1. **Export Cookies:**
   - Install a browser extension such as [Get cookies.txt locally](https://chrome.google.com/webstore/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc) or [Cookie-Editor](https://cookie-editor.com/).
   - Go to [YouTube](https://www.youtube.com) and ensure you are logged in.
   - Export your cookies in **Netscape format**.

2. **Place File in Workspace:**
   - Save or copy the exported file directly to:
     ```
     config/cookie/cookie.txt
     ```
   - *Note:* The application automatically validates that the file has a valid Netscape format and future expiration dates.

3. **Run the Application:**
   - The application detects and validates `config/cookie/cookie.txt` on startup and caches validation metadata without exposing tokens in logs.

---

## 🚂 Railway Deployment (Production)

To deploy cookies to Railway **without committing sensitive tokens to GitHub**:

### Method A: Railway Persistent Volume (Recommended)

1. In your Railway project dashboard, select your **KiwiMusic service**.
2. Navigate to **Settings** → **Volumes** → **Add Volume**.
3. Set the **Mount Path** to:
   ```
   /app/config/cookie
   ```
4. Once the volume is attached, open the Railway CLI or web terminal:
   ```bash
   railway run bash
   ```
   Or upload your `cookie.txt` to the mounted volume directory `/app/config/cookie/cookie.txt`.
5. Set environment variable in Railway Dashboard:
   ```env
   YOUTUBE_COOKIES_FILE=/app/config/cookie/cookie.txt
   ```
6. Redeploy or restart the service.

### Method B: Railway Config-as-Secret / Custom Mount

If mounting to `/mnt/cookie/cookie.txt` or another directory:
1. Mount your secret file or volume to `/mnt/cookie/cookie.txt`.
2. In Railway Dashboard under **Variables**, set:
   ```env
   YOUTUBE_COOKIES_FILE=/mnt/cookie/cookie.txt
   ```
3. The application will immediately resolve and authenticate `yt-dlp` from that location.

---

## 🔒 Security Best Practices

- **Never Commit Cookies:** Both `.gitignore` and `.dockerignore` are configured to ignore `config/cookie/*`, `config/cookies/*.txt`, `cookies.txt`, `*.cookie`, `*.cookies`, and `*.txt`.
- **Zero Token Leakage:** The application never logs cookie contents, session hashes, or authentication headers. Only cookie count, file size, and expiration health are logged.
- **Docker Layer Hygiene:** Cookies are excluded from the Docker build context and are never baked into image layers.

---

## 🛠️ Troubleshooting

### "Sign in to confirm you're not a bot" or "The page needs to be reloaded"
- **Cause:** YouTube cookies have expired or session tokens were invalidated by Google.
- **Solution:** Re-export fresh cookies from your browser and update `config/cookie/cookie.txt` (local) or your Railway mounted file.
- **Automatic Fallback:** The downloader automatically falls back to unauthenticated streaming if the cookie file fails, preventing total playback failure.
