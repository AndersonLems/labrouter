#!/usr/bin/env python3
"""Explicit live acceptance check: changes only the lab SSID, then restores it."""
import http.cookiejar,json,pathlib,subprocess,urllib.request,time
root=pathlib.Path(__file__).resolve().parent.parent
jar=http.cookiejar.CookieJar();opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));csrf=''
def api(path,method='GET',data=None):
 req=urllib.request.Request('http://127.0.0.1:8088/api/'+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
 return json.load(opener.open(req,timeout=65))
password=subprocess.check_output(['sudo','-n','cat','/var/lib/lab-router/initial-admin-password'],text=True).strip()
csrf=api('auth/login','POST',{'password':password})['csrf']
old=api('wifi');dashboard=api('dashboard');report={'mode':dashboard['mode'],'helper_available':dashboard['network']['available'],'initial_ssid':old['ssid']}
assert dashboard['mode']=='live' and dashboard['network']['available']
try:
 changed=dict(old,ssid='LAB-TR069-VALIDACAO')
 api('wifi','PUT',changed)
 info=subprocess.check_output(['iw','dev','ap-tr069','info'],text=True)
 assert '\tssid LAB-TR069-VALIDACAO\n' in info
 report['physical_ssid_changed']=True
 report['wan_during_change']=subprocess.check_output(['iw','dev','wlp2s0','link'],text=True).splitlines()[:3]
finally:
 api('wifi','PUT',old)
report['restored_ssid']=api('wifi')['ssid'];report['physical_restored']='\tssid '+old['ssid']+'\n' in subprocess.check_output(['iw','dev','ap-tr069','info'],text=True)
report['services']=api('dashboard')['services'];report['leases']=api('dhcp/leases')
(root/'artifacts/live-validation.json').write_text(json.dumps(report,indent=2,ensure_ascii=False))
print(json.dumps(report,ensure_ascii=False))
