#!/usr/bin/env python3
"""Enable test interval (60s) and verify two real PERIODIC sessions against configured ACS.
Uses initial local administrative credential; enter changed credentials in the UI instead.
"""
import http.cookiejar,json,pathlib,subprocess,time,urllib.request
root=pathlib.Path(__file__).resolve().parent.parent
jar=http.cookiejar.CookieJar();opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));csrf=''
def api(path,method='GET',data=None):
 req=urllib.request.Request('http://127.0.0.1:8088/api/'+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
 return json.load(opener.open(req,timeout=15))
password=subprocess.check_output(['sudo','-n','cat','/var/lib/lab-router/initial-admin-password'],text=True).strip()
csrf=api('auth/login','POST',{'password':password})['csrf']
baseline=max([r['id'] for r in api('tr069/sessions')]+[0]);c=api('tr069/config');c.update(enabled=True,periodic_enable=True,interval=60)
api('tr069/config','PUT',c)
deadline=time.monotonic()+140
while time.monotonic()<deadline:
 records=api('tr069/sessions');periodic=[r for r in records if r['id']>baseline and r['action']=='Inform' and '2 PERIODIC' in r['result']]
 successes=[r for r in records if r['id']>baseline and r['action']=='session' and 'confirmados' in r['result']]
 if len(periodic)>=2 and len(successes)>=2:
  result={'configured_interval':60,'periodic_informs':periodic,'confirmed_sessions':successes,'status':api('tr069/status')}
  (root/'artifacts/periodic-beta.json').write_text(json.dumps(result,indent=2,ensure_ascii=False))
  print(json.dumps({'periodic_informs':len(periodic),'confirmed_sessions':len(successes),'state':result['status']['state'],'interval':60}));break
 time.sleep(2)
else:
 status=api('tr069/status');(root/'artifacts/periodic-beta.json').write_text(json.dumps({'error':'not enough confirmed periodic sessions in test window','status':status},indent=2));print('Ainda não foram confirmadas duas sessões periódicas; consulte artifacts/periodic-beta.json');raise SystemExit(1)
