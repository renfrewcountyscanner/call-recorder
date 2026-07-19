#!/usr/bin/python3
"""Chromium acceptance test for the page's real ended-event playback handler."""
import os, sys, time
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By

options = Options(); options.add_argument("--headless"); options.add_argument("--no-sandbox"); options.add_argument("--disable-gpu")
driver = webdriver.Chrome(options=options)
errors = []
try:
    driver.get("http://127.0.0.1:18080/")
    for _ in range(30):
        if len(driver.find_elements(By.TAG_NAME, "audio")) >= 3: break
        time.sleep(.25)
    audios = driver.find_elements(By.TAG_NAME, "audio")
    assert len(audios) >= 3, "expected three synthetic calls"
    result = driver.execute_script("""
      window.__plays=[];
      HTMLMediaElement.prototype.play=function(){window.__plays.push(this.src); return Promise.resolve();};
      const a=[...document.querySelectorAll('audio')]; a[0].play(); a[0].dispatchEvent(new Event('ended'));
      a[1].dispatchEvent(new Event('ended')); return window.__plays;
    """)
    assert len(result) == 3, "ended events did not advance sequential playback"
    driver.find_element(By.CSS_SELECTOR, "input[name=system]").send_keys("system-a")
    time.sleep(1)
    assert len(driver.find_elements(By.TAG_NAME, "audio")) >= 1, "filtered queue empty"
    print("browser sequential test passed")
finally:
    driver.quit()
