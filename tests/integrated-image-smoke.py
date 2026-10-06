"""Isolated native image acceptance; never reads production credentials/data."""
import io,json,os,re,secrets,subprocess,time,tempfile,urllib.request,urllib.error,urllib.parse
from pathlib import Path
from PIL import Image
scratch=os.environ.get('OPENLIST_INTEGRATED_TEST_SCRATCH')
if not scratch: raise SystemExit('Set OPENLIST_INTEGRATED_TEST_SCRATCH to a private scratch directory (root required).')
root=Path(tempfile.mkdtemp(prefix='native-openlist-acceptance-',dir=scratch))
image_tag=os.environ.get('OPENLIST_INTEGRATED_TEST_IMAGE','openlist-custom:verification')
data=root/'data'; data.mkdir(mode=0o700);os.chown(data,1001,1001)
name='openlist-custom-acceptance-'+secrets.token_hex(4)
report={'isolated':True,'production_modified':False,'checks':[]}
def docker(*args):return subprocess.check_output(['docker',*args],stderr=subprocess.STDOUT).decode()
def request(path,method='GET',body=None,headers=None):
 req=urllib.request.Request(base+path,data=body,headers=headers or {},method=method)
 try:r=urllib.request.urlopen(req,timeout=30)
 except urllib.error.HTTPError as e:r=e
 b=r.read();return r.status,r.headers,b
try:
 docker('run','-d','--name',name,'--restart','no','-p','127.0.0.1::5244','-e','SITE_URL=https://openlist-test.example','-v',str(data)+':/opt/openlist/data',image_tag)
 port=docker('port',name,'5244/tcp').strip().rsplit(':',1)[1];base='http://127.0.0.1:'+port
 for _ in range(70):
  try:
   status,_,body=request('/icon/api/catalog')
   if status==200:break
  except Exception:pass
  time.sleep(1)
 else:raise RuntimeError('runtime did not become ready')
 report['checks'].append('native catalog and private store startup')
 status,_,body=request('/ping');assert status==200 and body==b'pong'
 status,_,body=request('/api/public/settings');assert json.loads(body)['code']==200
 status,_,html=request('/');assert status==200 and b'/assets/index-' in html
 paths=set(re.findall(rb'/assets/[A-Za-z0-9_.-]+',html))
 for raw in paths:
  path=raw.decode();s,h,b=request(path);assert s==200 and 'text/html' not in h.get('Content-Type','')
 report['checks'].append('official ping/public settings and embedded custom frontend assets')
 for path in ['/icon/catalog.db','/icon/originals/a.png','/icon/not-found']:
  s,h,b=request(path);assert s==404 and 'application/json' in h.get('Content-Type','')
 report['checks'].append('private paths JSON404, never SPA HTML')
 # Only this brand-new isolated instance initial administrator credential is used.
 logs=docker('logs',name)
 (root/'startup.log').write_text(re.sub(r'initial password is:\s*\S+', 'initial password is: [REDACTED]', logs));(root/'startup.log').chmod(0o600)
 match=re.search(r'initial password is:\s*(\S+)',logs,re.I)
 if not match:raise RuntimeError('isolated bootstrap credential not found; no production lookup')
 pwd=match.group(1)
 s,h,b=request('/api/auth/login','POST',json.dumps({'username':'admin','password':pwd}).encode(),{'Content-Type':'application/json'})
 payload=json.loads(b);assert payload['code']==200;token=payload['data']['token']
 auth={'Authorization':token,'Origin':'https://openlist-test.example'}
 image=Image.new('RGBA',(60,40),(90,40,200,170));buf=io.BytesIO();image.save(buf,format='PNG');png=buf.getvalue()
 headers={**auth,'Content-Type':'application/octet-stream','X-Upload-Id':'ab'*16,'X-Icon-Name':'test.png'}
 s,h,b=request('/icon/api/upload','POST',png,{k:v for k,v in headers.items() if k!='Authorization'});assert s==401
 s,h,b=request('/icon/api/upload','POST',png,{**headers,'Origin':'https://evil.example'});assert s==403
 s,h,b=request('/icon/api/upload','POST',png,headers);assert s==200,(s,b);result=json.loads(b);asset=result['data']['asset'];assetid=asset['id']
 s,h,b=request('/icon/api/upload','POST',png,headers);assert s==200 and json.loads(b)['data']['reused'] is True
 s,h,b=request('/icon/api/catalog');catalog=json.loads(b);assert any(a['id']==assetid for a in catalog['data']['assets'])
 s,h,b=request('/icon/assets/'+assetid+'.webp');assert s==200 and h['Content-Type']=='image/webp';Image.open(io.BytesIO(b)).load();assert Image.open(io.BytesIO(b)).size==(256,256)
 report['checks'].append('real native bootstrap login; upload/idempotency/catalog/WebP decode; missing auth401/foreign origin403')
 # Fresh DB checks must reject the same still-valid JWT as privileges change.
 import sqlite3
 database=sqlite3.connect(data/'data.db')
 row=database.execute("SELECT id,pwd_ts FROM x_users WHERE username='admin'").fetchone();assert row
 uid,pwdts=row
 database.execute('UPDATE x_users SET disabled=1 WHERE id=?',(uid,));database.commit()
 s,h,b=request('/icon/api/upload','POST',png,headers);assert s==403
 database.execute('UPDATE x_users SET disabled=0,role=0 WHERE id=?',(uid,));database.commit()
 s,h,b=request('/icon/api/upload','POST',png,headers);assert s==403
 database.execute('UPDATE x_users SET role=2,pwd_ts=? WHERE id=?',(pwdts+1,uid));database.commit()
 s,h,b=request('/icon/api/upload','POST',png,headers);assert s==401
 database.execute('UPDATE x_users SET pwd_ts=? WHERE id=?',(pwdts,uid));database.commit()
 native=database.execute("SELECT value FROM x_setting_items WHERE key='token'").fetchone()[0]
 s,h,b=request('/icon/api/upload','POST',png,{**headers,'Authorization':native});assert s==200
 s,h,b=request('/api/admin/setting/reset_token','POST',headers=auth);assert json.loads(b)['code']==200,(s,b)
 s,h,b=request('/icon/api/upload','POST',png,{**headers,'Authorization':native});assert s==401
 s,h,b=request('/api/auth/logout',headers=auth);assert json.loads(b)['code']==200
 s,h,b=request('/icon/api/upload','POST',png,headers);assert s==401
 s,h,b=request('/api/auth/login','POST',json.dumps({'username':'admin','password':pwd}).encode(),{'Content-Type':'application/json'});token=json.loads(b)['data']['token'];auth['Authorization']=token;headers['Authorization']=token
 database.close()
 report['checks'].append('fresh DB disable/demotion403/password revision401; rotated native token401; logged-out JWT401')
 # Save a reference via the real native settings API, never fixture auth hooks.
 s,h,b=request('/api/admin/setting/get?key=customize_head',headers=auth);item=json.loads(b)['data']; original=item['value']
 config={'version':1,'selections':{'folder':assetid}};encoded=urllib.parse.quote(json.dumps(config,separators=(',',':')))
 item['value']=original+'\n<!-- OPENLIST-ICON-THEME-START -->\n<meta id="openlist-icon-config" content="'+encoded+'">\n<!-- OPENLIST-ICON-THEME-END -->'
 s,h,b=request('/api/admin/setting/save','POST',json.dumps([item]).encode(),{**auth,'Content-Type':'application/json'});assert json.loads(b)['code']==200
 s,h,b=request('/icon/api/assets/'+assetid,'DELETE',headers=auth);assert s==409,(s,b)
 item['value']=original;s,h,b=request('/api/admin/setting/save','POST',json.dumps([item]).encode(),{**auth,'Content-Type':'application/json'});assert json.loads(b)['code']==200
 docker('restart',name)
 port=docker('port',name,'5244/tcp').strip().rsplit(':',1)[1];base='http://127.0.0.1:'+port
 for _ in range(50):
  try:
   s,h,b=request('/icon/api/catalog')
   if s==200 and json.loads(b).get('code')==200:break
  except Exception:pass
  time.sleep(1)
 assert json.loads(b).get('data') is not None, ('catalog after restart',s,b)
 assert any(a['id']==assetid for a in json.loads(b)['data']['assets'])
 # Native token cache clears on restart: obtain a fresh login using isolated bootstrap account.
 s,h,b=request('/api/auth/login','POST',json.dumps({'username':'admin','password':pwd}).encode(),{'Content-Type':'application/json'});token=json.loads(b)['data']['token'];auth['Authorization']=token
 s,h,b=request('/icon/api/assets/'+assetid,'DELETE',headers=auth);assert s==200,(s,b)
 s,h,b=request('/icon/api/catalog');assert not json.loads(b)['data']['assets']
 s,h,b=request('/icon/assets/'+assetid+'.webp');assert s==404
 report['checks'].append('native settings saved reference -> delete409; restart preserved active asset; unreferenced delete/readback404')
 processes=docker('top',name,'-eo','pid,comm,args');assert 'python' not in processes and 'nginx' not in processes;report['processes']=processes
 report['checks'].append('single native Go process, no Python/nginx sidecar')
 report['successful']=True
finally:
 try:
  raw=docker('logs',name)
  raw=re.sub(r'initial password is:\s*\S+', 'initial password is: [REDACTED]',raw)
  (root/'final.log').write_text(raw)
 except Exception:pass
 try:docker('rm','-f',name)
 except Exception:pass
 (root/'report.json').write_text(json.dumps(report,indent=2));print(json.dumps({'report':str(root/'report.json'),'checks':report['checks'],'successful':report.get('successful',False)},ensure_ascii=False))
