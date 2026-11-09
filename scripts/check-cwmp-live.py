#!/usr/bin/env python3
"""Controlled ACS sends real SOAP SPV to change and restore the physical lab SSID.
Runs a single live CWMP client as lab-router, temporarily pausing the periodic daemon.
"""
import http.cookiejar,json,pathlib,subprocess,threading,urllib.request,xml.etree.ElementTree as ET
from http.server import BaseHTTPRequestHandler,HTTPServer
root=pathlib.Path(__file__).resolve().parent.parent
jar=http.cookiejar.CookieJar();opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));csrf=''
def api(path,method='GET',data=None):
 req=urllib.request.Request('http://127.0.0.1:8088/api/'+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Content-Type':'application/json','X-CSRF-Token':csrf})
 return json.load(opener.open(req,timeout=60))
def command(*args):return subprocess.check_output(args,text=True)
password=command('sudo','-n','cat','/var/lib/lab-router/initial-admin-password').strip()
csrf=api('auth/login','POST',{'password':password})['csrf'];old=api('tr069/config');wifi=api('wifi');step=0;report={'transport':'real SOAP/HTTP CWMP','original_ssid':wifi['ssid'],'steps':[]};path='InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID';new='LAB-CWMP-TESTE-REAL'
def envelope(body):return ('<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" xmlns:cwmp="urn:dslforum-org:cwmp-1-0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:soap-enc="http://schemas.xmlsoap.org/soap/encoding/"><s:Header><cwmp:ID s:mustUnderstand="1">test</cwmp:ID></s:Header><s:Body>'+body+'</s:Body></s:Envelope>').encode()
def read():return '<cwmp:GetParameterValues><ParameterNames soap-enc:arrayType="xsd:string[1]"><string>'+path+'</string></ParameterNames></cwmp:GetParameterValues>'
def write(ssid):return '<cwmp:SetParameterValues><ParameterList soap-enc:arrayType="cwmp:ParameterValueStruct[1]"><ParameterValueStruct><Name>'+path+'</Name><Value xsi:type="xsd:string">'+ssid+'</Value></ParameterValueStruct></ParameterList><ParameterKey>physical-test</ParameterKey></cwmp:SetParameterValues>'
def has_ssid(name):return '\tssid '+name+'\n' in command('iw','dev','ap-tr069','info')
class ACS(BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_POST(self):
  global step
  b=self.rfile.read(int(self.headers.get('Content-Length',0)));response=''
  try:
   rpc=list(ET.fromstring(b).find('{http://schemas.xmlsoap.org/soap/envelope/}Body'))[0] if b else None
   tag=rpc.tag.split('}')[-1] if rpc is not None else ''
   if step==0:assert tag=='Inform';response='<cwmp:InformResponse><MaxEnvelopes>1</MaxEnvelopes></cwmp:InformResponse>'
   elif step==1:assert not b;response=read()
   elif step==2:assert tag=='GetParameterValuesResponse' and wifi['ssid'] in b.decode();response=write(new)
   elif step==3:assert tag=='SetParameterValuesResponse' and has_ssid(new);report['physical_change_confirmed']=True;response=read()
   elif step==4:assert tag=='GetParameterValuesResponse' and new in b.decode();report['cwmp_readback_confirmed']=True;response=write(wifi['ssid'])
   elif step==5:assert tag=='SetParameterValuesResponse' and has_ssid(wifi['ssid']);report['physical_restore_confirmed']=True
   else:raise AssertionError('unexpected step')
   report['steps'].append({'step':step,'rpc':tag});step+=1
   body=envelope(response) if response else b'';self.send_response(200 if body else 204);self.send_header('Content-Type','text/xml');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
  except Exception:
   report['error']='ACS validation failed at step '+str(step);self.send_error(500,'Validation failed')
server=HTTPServer(('127.0.0.1',0),ACS);threading.Thread(target=server.serve_forever,daemon=True).start()
subprocess.run(['sudo','-n','systemctl','stop','lab-router-cwmp'],check=True)
try:
 temporary=dict(old,url='http://127.0.0.1:'+str(server.server_port)+'/',enabled=True,periodic_enable=False);api('tr069/config','PUT',temporary)
 r=subprocess.run(['sudo','-n','runuser','-u','lab-router','--','env','LAB_DATA_DIR=/var/lib/lab-router','LAB_MODE=live','/usr/local/lib/lab-router/lab-router','inform-once'],capture_output=True,text=True,timeout=120)
 report['exit_code']=r.returncode;report['wan_preserved']='Connected to ' in command('iw','dev','wlp2s0','link');assert r.returncode==0 and step==6
finally:
 api('wifi','PUT',wifi);api('tr069/config','PUT',old);subprocess.run(['sudo','-n','systemctl','start','lab-router-cwmp'],check=True);server.shutdown();(root/'artifacts/cwmp-live-physical.json').write_text(json.dumps(report,indent=2,ensure_ascii=False));print(json.dumps(report,ensure_ascii=False))
