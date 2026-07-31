#!/usr/bin/python3
"""Automated accessibility checks with vendored axe-core against the
isolated callrecorder_it environment. Fails on critical violations."""
import json, time
from pathlib import Path
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By

BASE = "http://127.0.0.1:18080"
TOKEN = "synthetic-admin-token"
AXE = Path(__file__).with_name("vendor").joinpath("axe.min.js").read_text()

options = Options(); options.add_argument("--headless"); options.add_argument("--no-sandbox"); options.add_argument("--disable-gpu")
options.add_argument("--window-size=1366,900")
driver = webdriver.Chrome(options=options)

def wait_css(css, timeout=10):
    for _ in range(int(timeout * 4)):
        found = driver.find_elements(By.CSS_SELECTOR, css)
        if found: return found[0]
        time.sleep(.25)
    raise AssertionError(f"timed out waiting for {css}")

def audit(name, driver):
    driver.execute_script(AXE)
    result = driver.execute_async_script("""
      const done = arguments[arguments.length - 1];
      axe.run(document, {resultTypes: ['violations']}).then(r => done(r.violations));
    """)
    critical = [v for v in result if v.get("impact") == "critical"]
    serious = [v for v in result if v.get("impact") == "serious"]
    print(f"  {name}: {len(critical)} critical, {len(serious)} serious, {len(result) - len(critical) - len(serious)} other violations")
    for v in result:
        print(f"    [{v.get('impact')}] {v.get('id')}: {v.get('help')} ({len(v.get('nodes', []))} nodes)")
    return result

failures = []
try:
    driver.get(BASE + "/")
    wait_css("[data-play]")
    failures += audit("call list", driver)

    driver.execute_script("arguments[0].click()", driver.find_element(By.CSS_SELECTOR, ".time-link"))
    wait_css(".detail-hero")
    failures += audit("call detail", driver)

    driver.get(BASE + "/admin/login")
    wait_css(".login-card")
    failures += audit("admin login", driver)

    wait_css("input[name=username]").send_keys("admin")
    driver.find_element(By.CSS_SELECTOR, "input[name=password]").send_keys("testpassword")
    driver.find_element(By.CSS_SELECTOR, ".login-card button[type=submit]").click()
    wait_css("#alias-form")
    failures += audit("talkgroup administration", driver)

    driver.get(BASE + "/admin/radios")
    wait_css("#alias-form")
    failures += audit("radio administration", driver)

    driver.get(BASE + "/admin/transcription")
    wait_css("h1")
    failures += audit("transcription administration", driver)

    driver.get(BASE + "/admin/retention")
    wait_css("h1")
    failures += audit("retention administration", driver)

    driver.get(BASE + "/admin/retention/history")
    wait_css("h1")
    failures += audit("retention history", driver)

    # unauthorized state
    driver.delete_all_cookies()
    driver.get(BASE + "/admin/talkgroups")
    wait_css(".login-card")
    failures += audit("unauthorized administration", driver)
finally:
    driver.quit()

if failures:
    raise SystemExit(f"{len(failures)} accessibility violations")
print("accessibility checks passed")
