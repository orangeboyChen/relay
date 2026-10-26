package api

import (
	"fmt"
	"net/http"
)

const passkeyPage = `<!doctype html>
<html lang="en">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sim Deck Passkey</title>
<style>
  :root { color-scheme: light dark; font: -apple-system-body; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: Canvas; color: CanvasText; }
  main { width: min(32rem, calc(100% - 3rem)); text-align: center; }
  button { border: 0; border-radius: 999px; padding: .8rem 1.3rem; font: inherit; background: #007aff; color: white; }
  #error { color: #ff3b30; white-space: pre-wrap; }
</style>
<main>
  <h1 id="title">Sim Deck</h1>
  <p id="status">Waiting for passkey authorization…</p>
  <button id="retry" hidden>Try again</button>
  <p id="error"></p>
</main>
<script>
(() => {
  const params = new URLSearchParams(location.hash.slice(1));
  const mode = location.pathname.endsWith('/register') ? 'register' : 'login';
  const accessToken = params.get('access_token');
  const callback = params.get('callback');
  const callbackAllowed = callback === 'simdeck://passkey';
  const state = params.get('state') || '';
  const title = document.querySelector('#title');
  const status = document.querySelector('#status');
  const error = document.querySelector('#error');
  const retry = document.querySelector('#retry');
  title.textContent = mode === 'register' ? 'Register Passkey' : 'Sign In with Passkey';

  const base64 = value => {
    const bytes = new Uint8Array(value);
    let binary = '';
    bytes.forEach(byte => binary += String.fromCharCode(byte));
    return btoa(binary);
  };
  const base64URL = value => base64(value).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
  const decodeBase64 = value => {
    const binary = atob(value);
    return Uint8Array.from(binary, character => character.charCodeAt(0));
  };
  const decodeBase64URL = value => decodeBase64(value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4));
  const bytesToJSON = value => JSON.parse(new TextDecoder().decode(decodeBase64(value)));
  const jsonToBase64 = value => base64(new TextEncoder().encode(JSON.stringify(value)));

  async function rpc(procedure, body, token) {
    const headers = { 'Content-Type': 'application/json' };
    if (token) headers.Authorization = 'Bearer ' + token;
    const response = await fetch('/relay.communication.v1.AuthenticationService/' + procedure, {
      method: 'POST', headers, body: JSON.stringify(body)
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.message || payload.error?.message || 'Request failed (' + response.status + ')');
    return payload;
  }

  function publicKeyCreation(options) {
    const publicKey = options.publicKey;
    publicKey.challenge = decodeBase64URL(publicKey.challenge);
    publicKey.user.id = decodeBase64URL(publicKey.user.id);
    (publicKey.excludeCredentials || []).forEach(item => item.id = decodeBase64URL(item.id));
    return publicKey;
  }

  function publicKeyRequest(options) {
    const publicKey = options.publicKey;
    publicKey.challenge = decodeBase64URL(publicKey.challenge);
    (publicKey.allowCredentials || []).forEach(item => item.id = decodeBase64URL(item.id));
    return publicKey;
  }

  function registrationCredential(credential) {
    return {
      id: credential.id,
      rawId: base64URL(credential.rawId),
      type: credential.type,
      response: {
        clientDataJSON: base64URL(credential.response.clientDataJSON),
        attestationObject: base64URL(credential.response.attestationObject)
      }
    };
  }

  function assertionCredential(credential) {
    return {
      id: credential.id,
      rawId: base64URL(credential.rawId),
      type: credential.type,
      response: {
        clientDataJSON: base64URL(credential.response.clientDataJSON),
        authenticatorData: base64URL(credential.response.authenticatorData),
        signature: base64URL(credential.response.signature),
        userHandle: credential.response.userHandle ? base64URL(credential.response.userHandle) : undefined
      }
    };
  }

  function finish(result) {
    if (!callbackAllowed) return;
    const target = new URL(callback);
    target.hash = new URLSearchParams({ state, ...result }).toString();
    location.replace(target.toString());
  }

  function timestamp(value) {
    if (typeof value === 'string') return value;
    if (!value || value.seconds === undefined) return '';
    return new Date(Number(value.seconds) * 1000 + Number(value.nanos || 0) / 1000000).toISOString();
  }

  async function run() {
    status.textContent = mode === 'register' ? 'Create a passkey to protect Sim Deck.' : 'Choose your Sim Deck passkey.';
    if (mode === 'register' && !accessToken) throw new Error('The signed-in session is missing.');
    if (!window.PublicKeyCredential || !navigator.credentials) throw new Error('This browser does not support passkeys.');
    const started = await rpc(mode === 'register' ? 'BeginPasskeyRegistration' : 'BeginPasskeyLogin', {}, accessToken);
    const options = bytesToJSON(started.optionsJson);
    const credential = mode === 'register'
      ? await navigator.credentials.create({ publicKey: publicKeyCreation(options) })
      : await navigator.credentials.get({ publicKey: publicKeyRequest(options) });
    if (!credential) throw new Error('Passkey authorization was cancelled.');
    const credentialJSON = mode === 'register' ? registrationCredential(credential) : assertionCredential(credential);
    const finished = await rpc(
      mode === 'register' ? 'FinishPasskeyRegistration' : 'FinishPasskeyLogin',
      { ceremonyId: started.ceremonyId, credentialJson: jsonToBase64(credentialJSON) },
      accessToken
    );
    if (mode === 'register') {
      status.textContent = 'Passkey registered. Returning to Sim Deck…';
      finish({ status: 'success' });
    } else {
      status.textContent = 'Signed in. Returning to Sim Deck…';
      finish({
        status: 'success',
        access_token: finished.accessToken,
        refresh_token: finished.refreshToken,
        access_expires_at: timestamp(finished.accessExpiresAt),
        refresh_expires_at: timestamp(finished.refreshExpiresAt)
      });
    }
  }

  retry.onclick = () => { retry.hidden = true; error.textContent = ''; run().catch(showError); };
  function showError(reason) {
    status.textContent = 'Passkey could not be completed.';
    error.textContent = reason instanceof Error ? reason.message : String(reason);
    retry.hidden = false;
  }
  run().catch(showError);
})();
</script>
</html>`

func (router *router) passkeyPage(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(writer, passkeyPage)
}
