#!/usr/bin/python3
"""Capture sanitized screenshots from the isolated callrecorder_it
environment into tests/output/. Synthetic fixture data only."""
import time
from pathlib import Path
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By

BASE = "http://127.0.0.1:18080"
TOKEN = "synthetic-admin-token"
OUT = Path(__file__).with_name("output")
OUT.mkdir(exist_ok=True)

def make_driver(width, height):
    options = Options()
    options.add_argument("--headless"); options.add_argument("--no-sandbox"); options.add_argument("--disable-gpu")
    options.add_argument(f"--window-size={width},{height}")
    options.add_argument("--force-device-scale-factor=1")
    return webdriver.Chrome(options=options)

def make_mobile_driver(width, height):
    """Chrome clamps --window-size near 500px; use CDP device metrics for real mobile widths."""
    driver = make_driver(800, height)
    driver.execute_cdp_cmd("Emulation.setDeviceMetricsOverride", {"width": width, "height": height, "deviceScaleFactor": 1, "mobile": True})
    return driver

def wait_css(driver, css, timeout=10):
    for _ in range(int(timeout * 4)):
        found = driver.find_elements(By.CSS_SELECTOR, css)
        if found: return found[0]
        time.sleep(.25)
    raise AssertionError(f"timed out waiting for {css}")

def shot(driver, name):
    driver.save_screenshot(str(OUT / f"{name}.png"))
    print(f"  {name}.png")

d = make_driver(1366, 900)
try:
    d.get(BASE + "/")
    wait_css(d, "[data-play]")
    shot(d, "desktop-call-list-dark")

    d.execute_script("document.querySelector('details.filters').setAttribute('open','')")
    d.find_element(By.CSS_SELECTOR, "input[name=system]").send_keys("system-a")
    wait_css(d, ".chip")
    shot(d, "desktop-filter-panel")

    d.execute_script("HTMLMediaElement.prototype.play=function(){return Promise.resolve();};")
    d.execute_script("document.querySelector('[data-play]').click()")
    time.sleep(.4)
    shot(d, "desktop-playing-dark")

    d.execute_script("arguments[0].click()", d.find_element(By.CSS_SELECTOR, ".time-link"))
    wait_css(d, ".detail-hero")
    shot(d, "desktop-call-detail-dark")

    # light theme
    d.get(BASE + "/")
    wait_css(d, "[data-play]")
    d.execute_script("window.__setTheme('light')")
    time.sleep(.3)
    shot(d, "desktop-call-list-light")
    d.execute_script("window.__setTheme('dark')")

    # admin
    d.get(BASE + "/admin/login")
    wait_css(d, ".login-card")
    shot(d, "desktop-admin-login")
    d.find_element(By.CSS_SELECTOR, "input[name=username]").send_keys("admin")
    d.find_element(By.CSS_SELECTOR, "input[name=password]").send_keys("testpassword")
    d.find_element(By.CSS_SELECTOR, ".login-card button[type=submit]").click()
    wait_css(d, "#alias-form")
    shot(d, "desktop-admin-talkgroups")
    d.get(BASE + "/admin/radios")
    wait_css(d, "#alias-form")
    shot(d, "desktop-admin-radios")
    d.get(BASE + "/admin/retention")
    wait_css(d, "h1")
    shot(d, "desktop-admin-retention")
    d.get(BASE + "/admin/retention/history")
    wait_css(d, "h1")
    shot(d, "desktop-admin-history")
finally:
    d.quit()

m = make_mobile_driver(375, 800)
try:
    m.get(BASE + "/")
    wait_css(m, "[data-play]")
    shot(m, "mobile-call-list-dark")
    m.find_element(By.ID, "nav-toggle").click()
    time.sleep(.3)
    shot(m, "mobile-nav-open")
    m.execute_script("HTMLMediaElement.prototype.play=function(){return Promise.resolve();};")
    m.execute_script("document.querySelector('[data-play]').click()")
    time.sleep(.4)
    shot(m, "mobile-playing-dark")
    m.execute_script("window.__setTheme('light')")
    m.get(BASE + "/")
    wait_css(m, "[data-play]")
    shot(m, "mobile-call-list-light")
finally:
    m.quit()

print(f"screenshots written to {OUT}")
