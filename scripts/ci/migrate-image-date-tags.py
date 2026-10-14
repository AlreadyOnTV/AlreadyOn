"""Move date aliases without deleting source manifests or rebuilding images."""
import argparse,base64,hashlib,json,os,re,subprocess,time,urllib.request,urllib.parse,urllib.error
from pathlib import Path
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--apply', action='store_true')
args=parser.parse_args()
repository=os.environ['GITHUB_REPOSITORY']
assert repository=='AlreadyOnTV/AlreadyOn', 'Unexpected migration repository'
package=repository.lower();base='https://ghcr.io/v2/'+package+'/manifests/'
pat=subprocess.check_output(['gh','auth','token'],text=True).strip()
user=os.environ.get('GITHUB_ACTOR','x-access-token')
auth='Basic '+base64.b64encode((user+':'+pat).encode()).decode()
url='https://ghcr.io/token?'+urllib.parse.urlencode({'service':'ghcr.io','scope':'repository:'+package+':pull,push'})
with urllib.request.urlopen(urllib.request.Request(url,headers={'Authorization':auth})) as r:token=json.load(r)['token']
accept=', '.join(['application/vnd.oci.image.index.v1+json','application/vnd.oci.image.manifest.v1+json','application/vnd.docker.distribution.manifest.list.v2+json','application/vnd.docker.distribution.manifest.v2+json'])

def registry(ref,data=None,media=None):
    headers={'Authorization':'Bearer '+token,'Accept':accept}
    if media:headers['Content-Type']=media
    request=urllib.request.Request(base+ref,data=data,headers=headers,method='PUT' if data is not None else 'GET')
    with urllib.request.urlopen(request,timeout=60) as r:return r.read(),r.headers.get('Docker-Content-Digest')

def versions():
    pages=json.loads(subprocess.check_output(['gh','api','orgs/AlreadyOnTV/packages/container/alreadyon/versions?per_page=100','--paginate','--slurp'],text=True))
    return [x for page in pages for x in page]

originals=[{'tags':x['metadata']['container']['tags'],'name':x['name']} for x in versions()]
for original in originals:
    for old in original['tags']:
        if not re.fullmatch(r'20\d{2}-\d{2}-\d{2}',old):continue
        new=old[2:];digest=original['name']
        if not args.apply:
            print('Would rename:',old,'->',new,digest,flush=True);continue
        raw,got=registry(digest)
        assert got==digest and 'sha256:'+hashlib.sha256(raw).hexdigest()==digest,'Source digest mismatch'
        manifest=json.loads(raw);media=manifest['mediaType']
        try:_,existing=registry(new)
        except urllib.error.HTTPError as error:
            if error.code!=404:raise
            existing=None
        assert existing in (None,digest),'Short tag collision'
        _,published=registry(new,raw,media);assert published==digest,'Digest changed while retagging'
        print('Identical digest retagged:',old,'->',new,digest,flush=True)
        # GHCR deletion acts on versions, so move only the obsolete alias to an
        # empty unique manifest before deleting that placeholder version.
        placeholder=json.loads(raw)
        placeholder['annotations']={**placeholder.get('annotations',{}),'io.alreadyon.naming-migration':old+'-'+str(time.time_ns())}
        if 'manifests' in placeholder:placeholder['manifests']=[]
        else:placeholder['layers']=[]
        data=json.dumps(placeholder,separators=(',',':')).encode()
        expected='sha256:'+hashlib.sha256(data).hexdigest()
        _,temporary=registry(old,data,media);assert temporary==expected and temporary!=digest
        candidates=[x for x in versions() if x['name']==temporary and x['metadata']['container']['tags']==[old]]
        assert len(candidates)==1,'Cannot isolate obsolete alias for deletion'
        _,current=registry(new);assert current==digest,'Source alias moved unexpectedly'
        subprocess.run(['gh','api','--method','DELETE',f"orgs/AlreadyOnTV/packages/container/alreadyon/versions/{candidates[0]['id']}"],check=True,stdout=subprocess.DEVNULL)
        _,current=registry(new);assert current==digest
        try:registry(old)
        except urllib.error.HTTPError as error:assert error.code==404
        else:raise RuntimeError('Obsolete alias remains')
        print('Obsolete alias removed; source retained:',old,flush=True)
# Remove only orphan placeholders from this naming migration, never source images.
if args.apply:
    for version in versions():
        if version['metadata']['container']['tags']: continue
        raw,digest=registry(version['name']);manifest=json.loads(raw)
        if 'io.alreadyon.naming-migration' not in manifest.get('annotations',{}): continue
        if manifest.get('manifests') or manifest.get('layers'): continue
        subprocess.run(['gh','api','--method','DELETE',f"orgs/AlreadyOnTV/packages/container/alreadyon/versions/{version['id']}"],check=True,stdout=subprocess.DEVNULL)
    assert not any(re.fullmatch(r'20\d{2}-\d{2}-\d{2}', tag) for v in versions() for tag in v['metadata']['container']['tags'])
    print('All date aliases use YY-MM-DD; original digests preserved.')
