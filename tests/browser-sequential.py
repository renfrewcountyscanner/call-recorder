#!/usr/bin/python3
"""Chromium acceptance test for sequential playback with the shared player."""
import sys, time
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By

options = Options(); options.add_argument("--headless"); options.add_argument("--no-sandbox"); options.add_argument("--disable-gpu")
driver = webdriver.Chrome(options=options)
errors = []
try:
    driver.get("http://127.0.0.1:18080/")
    for _ in range(40):
        if len(driver.find_elements(By.CSS_SELECTOR, "[data-play]")) >= 3: break
        time.sleep(.25)
    buttons = driver.find_elements(By.CSS_SELECTOR, "[data-play]")
    assert len(buttons) >= 3, "expected three synthetic calls"
    driver.execute_script("""
      window.__plays=[];
      HTMLMediaElement.prototype.play=function(){window.__plays.push(this.getAttribute('src')); return Promise.resolve();};
    """)
    driver.execute_script("arguments[0].click()", buttons[0])
    time.sleep(.4)
    bar = driver.find_element(By.ID, "player-bar")
    assert bar.is_displayed(), "player bar not visible after play"
    row = driver.find_element(By.CSS_SELECTOR, ".call-row.is-playing")
    assert row is not None, "playing row not highlighted"
    driver.execute_script("document.getElementById('player-audio').dispatchEvent(new Event('ended'))")
    time.sleep(.3)
    driver.execute_script("document.getElementById('player-audio').dispatchEvent(new Event('ended'))")
    time.sleep(.3)
    plays = driver.execute_script("return window.__plays")
    assert len(plays) == 3, f"ended events did not advance sequential playback: {plays}"
    assert len(set(plays)) == 3, f"sequential playback repeated a call: {plays}"
    driver.find_element(By.CSS_SELECTOR, "input[name=system]").send_keys("system-a")
    for _ in range(30):
        if len(driver.find_elements(By.CSS_SELECTOR, "[data-play]")) >= 1: break
        time.sleep(.25)
    assert len(driver.find_elements(By.CSS_SELECTOR, "[data-play]")) >= 1, "filtered queue empty"
    log = [e for e in driver.get_log("browser") if e.get("level") == "SEVERE"]
    assert not log, f"browser console errors: {log}"
    print("browser sequential test passed")
finally:
    driver.quit()
