package api

import "net/http"

const passkeyPage = `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sim Deck Passkey</title><button id="go">Continue with passkey</button><p id="status"></p><script>
const status=document.getElementById('status'); const params=new URLSearchParams(location.search); const mode=params.get('mode')==='register'?'register':'login';
function decode(value){value=value.replace(/-/g,'+').replace(/_/g,'/');return Uint8Array.from(atob(value+'='.repeat((4-value.length%4)%4)),c=>c.charCodeAt(0)).buffer}
function encode(value){return btoa(String.fromCharCode(...new Uint8Array(value))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')}
function convert(value){if(Array.isArray(value))return value.map(convert);if(value&&typeof value==='object'){const result={};for(const [key,item] of Object.entries(value)){result[key]=['challenge','user','id'].includes(key)&&typeof item==='string'?decode(item):convert(item)}return result}return value}
document.getElementById('go').onclick=async()=>{try{const token=location.hash.startsWith('#token=')?decodeURIComponent(location.hash.slice(7)):'';const headers={'content-type':'application/json'};if(token)headers.authorization='Bearer '+token;const start=await (await fetch('/v1/auth/passkey/'+mode+'/options',{method:'POST',headers})).json();const options=convert(start.options);const credential=mode==='register'?await navigator.credentials.create(options):await navigator.credentials.get(options);const body={id:credential.id,rawId:encode(credential.rawId),type:credential.type,response:{clientDataJSON:encode(credential.response.clientDataJSON)}};if(mode==='register')body.response.attestationObject=encode(credential.response.attestationObject);else{body.response.authenticatorData=encode(credential.response.authenticatorData);body.response.signature=encode(credential.response.signature);if(credential.response.userHandle)body.response.userHandle=encode(credential.response.userHandle)}const finish=await fetch('/v1/auth/passkey/'+mode+'/finish?ceremony_id='+encodeURIComponent(start.ceremony_id),{method:'POST',headers,body:JSON.stringify(body)});if(!finish.ok)throw new Error('Passkey ceremony failed');const session=mode==='login'?await finish.json():{};location.href='simdeck://passkey#'+encodeURIComponent(JSON.stringify(session));}catch(error){status.textContent=error.message||'Passkey ceremony failed'}};
</script>`

func (router *router) passkeyPage(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write([]byte(passkeyPage))
}
