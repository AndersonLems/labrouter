#!/usr/bin/env python3
"""One real SOAP session, with network writes refused in observation mode.
Requires an already running local API. Never prints credentials or SOAP bodies.
"""
import http.cookiejar,json,pathlib,subprocess,urllib.request
root=pathlib.Path(__file__).resolve().parent.parent
jar=http.cookiejar.CookieJar();opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));csrf=''
def api(path,method='GET',data=None):
 payload=None if data is None else json.dumps(data).encode()
 req=urllib.request.Request('http://127.0.0.1:8088/api/'+path,data=payload,method=method,headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
 return json.load(opener.open(req,timeout=120))
csrf=api('auth/login','POST',{'password':(root/'var/initial-admin-password').read_text().strip()})['csrf']
old=api('tr069/config');active=dict(old,enabled=True)
try:
 api('tr069/config','PUT',active)
 r=subprocess.run([str(root/'bin/lab-router'),'inform-once'],cwd=root,capture_output=True,text=True,timeout=120)
 status=api('tr069/status');sessions=api('tr069/sessions')
 report={'exit_code':r.returncode,'status':status,'sessions':sessions,'scope':'SOAP session; observation mode refused network writes; no ACS panel credentials used'}
 (root/'artifacts/beta-session.json').write_text(json.dumps(report,indent=2))
 print(json.dumps({'exit_code':r.returncode,'state':status.get('state'),'http':status.get('http'),'error':status.get('error'),'last_success':status.get('last_success'),'received':status.get('received')},ensure_ascii=False))
finally:
 api('tr069/config','PUT',old)
