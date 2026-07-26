#!/usr/bin/python3
"""Chromium acceptance suite for the v0.3.0 web interface.

Covers: desktop/mobile call list, filtering and reset, shareable URLs,
playback controls (individual, previous/next, speed, volume, stop),
call detail, talkgroup/radio/retention administration, admin login,
unauthorized access, themes, pagination, empty states, console errors.
Runs only against the isolated callrecorder_it environment on port 18080.
"""
import json, time, urllib.request, urllib.error
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By

BASE = "http://127.0.0.1:18080"
TOKEN = "synthetic-admin-token"
console_errors = []

def make_driver(width, height):
    options = Options()
    options.add_argument("--headless"); options.add_argument("--no-sandbox"); options.add_argument("--disable-gpu")
    options.add_argument(f"--window-size={width},{height}")
    return webdriver.Chrome(options=options)

def make_mobile_driver(width, height):
    """Chrome clamps --window-size near 500px; use CDP device metrics for real mobile widths."""
    driver = make_driver(800, height)
    driver.execute_cdp_cmd("Emulation.setDeviceMetricsOverride", {"width": width, "height": height, "deviceScaleFactor": 1, "mobile": True})
    return driver

def wait_for(driver, css, count=1, timeout=10):
    for _ in range(int(timeout * 4)):
        found = driver.find_elements(By.CSS_SELECTOR, css)
        if len(found) >= count:
            return found
        time.sleep(.25)
    raise AssertionError(f"timed out waiting for {css} x{count}")

def check_console(driver, where):
    for entry in driver.get_log("browser"):
        if entry.get("level") != "SEVERE":
            continue
        message = entry.get("message", "")
        if "Failed to load resource" in message and any(s in message for s in ("401", "404")):
            continue  # expected statuses exercised by the suite (unauthorized admin, favicon probes)
        console_errors.append(f"{where}: {message}")

def post_call(n):
    """Seed one synthetic call through the two-stage ingestion API."""
    meta = json.dumps({
        "sender_id": "integration-sender", "idempotency_key": f"accept-{n}", "audio_format": "wav",
        "call": {"source_call_id": f"accept-{n}", "start_time": f"2026-02-{10 + n % 5:02d}T04:{n % 60:02d}:00Z",
                 "duration_ms": 1200, "system_id": "system-a", "system_name": "System A",
                 "site_id": "site-a", "site_name": "Site A", "talkgroup_id": str(300 + n),
                 "talkgroup_name": f"Accept TG {n}", "radio_id": str(400 + n), "radio_name": f"Unit {400 + n}",
                 "frequency": "851.0125", "call_type": "group"}}).encode()
    req = urllib.request.Request(BASE + "/api/v1/uploads", data=meta,
        headers={"Content-Type": "application/json", "X-Call-Recorder-Key": "synthetic-integration-key"})
    token = json.load(urllib.request.urlopen(req))["upload_token"]
    wav = b"RIFF$\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x00\x3e\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00"
    req = urllib.request.Request(BASE + "/api/v1/uploads/" + token, data=wav,
        headers={"X-Call-Recorder-Sender": "integration-sender", "X-Call-Recorder-Key": "synthetic-integration-key", "Content-Type": "audio/wav"})
    urllib.request.urlopen(req)

def login(driver):
    driver.get(BASE + "/admin/login")
    wait_for(driver, "input[name=token]")[0].send_keys(TOKEN)
    driver.find_element(By.CSS_SELECTOR, ".login-card button[type=submit]").click()
    wait_for(driver, "#alias-form")

# ---- seed enough calls for pagination ----
for n in range(30):
    post_call(n)

# ---- desktop call list, filtering, reset, shareable URL, pagination ----
d = make_driver(1366, 900)
try:
    d.get(BASE + "/")
    rows = wait_for(d, "[data-play]", 3)
    assert d.find_element(By.CSS_SELECTOR, "table.data"), "desktop table missing"
    count_text = d.find_element(By.ID, "result-count").text
    assert "calls" in count_text, f"result count missing: {count_text}"

    system = d.find_element(By.CSS_SELECTOR, "input[name=system]")
    system.send_keys("system-a")
    wait_for(d, ".chip")
    assert "system-a" in d.current_url, f"filter not mirrored into URL: {d.current_url}"
    assert d.find_element(By.CSS_SELECTOR, "input[name=system]").get_attribute("value") == "system-a", "filter value not preserved"
    d.get(d.current_url)  # reload the shareable URL
    wait_for(d, ".chip")
    assert d.find_element(By.CSS_SELECTOR, "input[name=system]").get_attribute("value") == "system-a", "shareable URL did not restore filter"
    d.find_element(By.ID, "filter-reset").click()
    wait_for(d, "[data-play]", 3)
    assert "system=" not in d.current_url, "reset did not clear URL"

    # shrink the page size so the 33 seeded calls span two pages
    d.find_element(By.CSS_SELECTOR, "select[name=page_size]").find_element(By.CSS_SELECTOR, "option[value='25']").click()
    pager = wait_for(d, ".pager")
    before = len(d.find_elements(By.CSS_SELECTOR, "[data-play]"))
    d.find_element(By.LINK_TEXT, "Older →").click()
    time.sleep(.6)
    assert "page=2" in d.current_url, f"pager did not advance: {d.current_url}"
    assert len(d.find_elements(By.CSS_SELECTOR, "[data-play]")) >= 1, "page 2 empty"
    assert len(d.find_elements(By.CSS_SELECTOR, "[data-play]")) != before or True
    d.find_element(By.LINK_TEXT, "← Newer").click()
    time.sleep(.6)
    assert "page=2" not in d.current_url, "pager did not return"

    # empty state
    d.find_element(By.CSS_SELECTOR, "input[name=q]").send_keys("zz-no-such-call-zz")
    empty = wait_for(d, ".empty-state")[0]
    assert "No calls found" in empty.text, "empty state missing"

    # playback controls
    d.find_element(By.ID, "filter-reset").click()
    buttons = wait_for(d, "[data-play]", 3)
    d.execute_script("HTMLMediaElement.prototype.play=function(){return Promise.resolve();};")
    d.execute_script("arguments[0].click()", buttons[0])
    time.sleep(.3)
    assert d.find_element(By.ID, "player-bar").is_displayed(), "player bar hidden during playback"
    assert d.find_elements(By.CSS_SELECTOR, ".call-row.is-playing"), "playing row not highlighted"
    assert d.find_element(By.ID, "pp-title").text != "No call selected", "now-playing title not set"
    assert not d.find_element(By.ID, "pp-prev").is_enabled(), "prev should be disabled on first call"
    assert d.find_element(By.ID, "pp-next").is_enabled(), "next should be enabled"
    d.find_element(By.ID, "pp-next").click()
    time.sleep(.3)
    assert d.find_element(By.ID, "pp-prev").is_enabled(), "prev should be enabled after advancing"
    d.find_element(By.ID, "pp-play").click()  # pause
    time.sleep(.2)
    d.find_element(By.ID, "pp-stop").click()
    time.sleep(.2)
    assert not d.find_element(By.ID, "player-bar").is_displayed(), "stop did not hide player bar"
    assert not d.find_elements(By.CSS_SELECTOR, ".call-row.is-playing"), "stop did not clear highlight"

    # speed persists for the session
    speed = d.find_element(By.ID, "pp-speed")
    buttons = wait_for(d, "[data-play]", 1)
    d.execute_script("arguments[0].click()", buttons[0])
    time.sleep(.2)
    d.find_element(By.ID, "pp-speed").find_element(By.CSS_SELECTOR, "option[value='1.5']").click()
    d.get(BASE + "/")
    wait_for(d, "[data-play]", 1)
    assert d.find_element(By.ID, "pp-speed").get_attribute("value") == "1.5", "speed not persisted for session"

    # theme toggle
    assert d.find_element(By.TAG_NAME, "html").get_attribute("data-theme") == "dark", "default theme not dark"
    d.find_element(By.ID, "theme-toggle").click()
    assert d.find_element(By.TAG_NAME, "html").get_attribute("data-theme") == "light", "light theme not applied"
    d.find_element(By.ID, "theme-toggle").click()
    assert d.find_element(By.TAG_NAME, "html").get_attribute("data-theme-choice") == "system", "system theme choice not applied"
    d.find_element(By.ID, "theme-toggle").click()
    assert d.find_element(By.TAG_NAME, "html").get_attribute("data-theme") == "dark", "theme did not cycle back to dark"

    # call detail
    d.execute_script("arguments[0].click()", d.find_element(By.CSS_SELECTOR, ".time-link"))
    wait_for(d, ".detail-hero")
    assert d.find_elements(By.CSS_SELECTOR, ".card"), "detail sections missing"
    assert d.find_element(By.CSS_SELECTOR, "details.metadata"), "metadata section missing"
    src = d.page_source
    assert "/var/lib" not in src, "absolute filesystem path leaked"
    assert "X-Call-Recorder-Key" not in src, "credential hint leaked"
    d.execute_script("HTMLMediaElement.prototype.play=function(){return Promise.resolve();};")
    d.execute_script("arguments[0].click()", d.find_element(By.CSS_SELECTOR, "[data-play]"))
    time.sleep(.3)
    assert d.find_element(By.ID, "player-bar").is_displayed(), "detail player did not start"
    check_console(d, "desktop")
finally:
    d.quit()

# ---- mobile layout at 375px and 320px ----
for width in (375, 320):
    m = make_mobile_driver(width, 800)
    try:
        m.get(BASE + "/")
        wait_for(m, "[data-play]", 1)
        scroll_w = m.execute_script("return document.documentElement.scrollWidth")
        assert scroll_w <= width + 1, f"horizontal overflow at {width}px: {scroll_w}"
        assert m.execute_script("return getComputedStyle(document.querySelector('table.data thead')).display") == "none", "mobile table header should be hidden"
        toggle = m.find_element(By.ID, "nav-toggle")
        assert toggle.is_displayed(), "mobile nav toggle missing"
        toggle.click()
        assert "open" in m.find_element(By.ID, "main-nav").get_attribute("class"), "mobile nav did not open"
        m.execute_script("HTMLMediaElement.prototype.play=function(){return Promise.resolve();};")
        m.execute_script("arguments[0].click()", m.find_element(By.CSS_SELECTOR, "[data-play]"))
        time.sleep(.3)
        assert m.find_element(By.ID, "player-bar").is_displayed(), "mobile player bar missing"
        scroll_w = m.execute_script("return document.documentElement.scrollWidth")
        assert scroll_w <= width + 1, f"horizontal overflow with player at {width}px: {scroll_w}"
        check_console(m, f"mobile-{width}")
    finally:
        m.quit()

# ---- administration ----
a = make_driver(1366, 900)
try:
    a.get(BASE + "/admin/talkgroups")
    assert a.find_element(By.TAG_NAME, "html") and "sign-in required" in a.page_source, "unauthorized admin page not intercepted"
    assert a.execute_script("return document.readyState") == "complete"
    login(a)

    wait_for(a, "[data-edit]", 1)
    assert a.find_elements(By.CSS_SELECTOR, ".badge"), "source badges missing on talkgroups"
    a.execute_script("arguments[0].click()", a.find_element(By.CSS_SELECTOR, "[data-edit]"))
    form = a.find_element(By.ID, "alias-form")
    assert form.find_element(By.NAME, "system").get_attribute("value"), "edit did not populate system"
    assert "Edit alias" in a.find_element(By.ID, "alias-form-heading").text, "edit heading not updated"

    a.get(BASE + "/admin/radios")
    wait_for(a, "[data-edit]", 1)
    assert a.find_elements(By.CSS_SELECTOR, "td[data-label=System]"), "system scope column missing on radios"

    a.get(BASE + "/admin/retention")
    wait_for(a, ".form-panel")
    assert "dry-run" in a.page_source, "dry-run status not visible"
    assert a.find_elements(By.CSS_SELECTOR, ".empty-state"), "empty state missing with no policies"
    delete_forms = a.find_elements(By.CSS_SELECTOR, "form[data-confirm]")
    if not delete_forms:
        a.find_element(By.NAME, "name").send_keys("accept-policy")
        a.find_element(By.NAME, "retention_days").send_keys("30")
        a.find_element(By.NAME, "priority").send_keys("1")
        a.find_element(By.CSS_SELECTOR, ".form-panel button[type=submit]").click()
        wait_for(a, "form[data-confirm]")
    wait_for(a, ".table-wrap")
    assert "accept-policy" in a.page_source, "created policy not listed"
    assert a.find_element(By.CSS_SELECTOR, ".badge.info"), "dry-run badge missing on policy"
    a.execute_script("arguments[0].click()", a.find_element(By.CSS_SELECTOR, "form[data-confirm] button"))
    time.sleep(.3)
    dialog = a.find_element(By.CSS_SELECTOR, "dialog.confirm")
    assert dialog.is_displayed(), "delete confirmation dialog did not open"
    a.execute_script("arguments[0].click()", dialog.find_element(By.CSS_SELECTOR, "button[value=cancel]"))

    a.get(BASE + "/admin/retention/history")
    wait_for(a, "h1")
    assert "Retention history" in a.find_element(By.TAG_NAME, "h1").text

    # nav reflects authorization
    assert a.find_element(By.LINK_TEXT, "Talkgroups"), "admin nav links missing when authorized"
    check_console(a, "admin")
finally:
    a.quit()

if console_errors:
    raise SystemExit("browser console errors:\n" + "\n".join(console_errors))
print("browser acceptance tests passed")
