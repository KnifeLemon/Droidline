import json, hashlib, hmac, base64, struct
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

def b64u(b): return base64.urlsafe_b64encode(b).rstrip(b'=').decode()
def priv(n):
    # Deterministic test keys derived from a label, never used outside tests.
    d = int.from_bytes(hashlib.sha256(n.encode()).digest(), 'big') % (2**256 - 2**224 - 1) + 1
    return ec.derive_private_key(d, ec.SECP256R1())
def pub(k): return k.public_key().public_bytes(serialization.Encoding.X962, serialization.PublicFormat.UncompressedPoint)
def dbytes(k): return k.private_numbers().private_value.to_bytes(32,'big')
def ecdh(a, b): return a.exchange(ec.ECDH(), b.public_key())
def hkdf_extract(salt, ikm): return hmac.new(salt, ikm, hashlib.sha256).digest()
def hkdf_expand(prk, info, n):
    out=b''; t=b''; i=1
    while len(out)<n:
        t=hmac.new(prk, t+info+bytes([i]), hashlib.sha256).digest(); out+=t; i+=1
    return out[:n]

ps, ss, pe, se = priv('phone static'), priv('server static'), priv('phone eph'), priv('server eph')
nonce_c = bytes(range(16)); nonce_s = bytes(range(16,32))
token = bytes(range(100,116))

def case(mode, with_token):
    l1 = {"hs":1,"proto":1,"mode":mode,"device":"a1b2c3d4","eph":b64u(pub(pe)),"nonce":b64u(nonce_c)}
    if mode != 'auth': l1["pub"] = b64u(pub(ps))
    if mode == 'pair_qr': l1["tid"] = "t0k3n1d0"
    line1 = json.dumps(l1, separators=(',',':'), ensure_ascii=False)
    line2 = json.dumps({"hs":1,"proto":1,"server":"k3j9d0a2mq","status":"ok","eph":b64u(pub(se)),"nonce":b64u(nonce_s)}, separators=(',',':'))
    th = hashlib.sha256(line1.encode()+b"\n"+line2.encode()).digest()
    ikm = ecdh(pe, se) + ecdh(ps, ss) + (token if with_token else b'')
    prk = hkdf_extract(th, ikm)
    k_up = hkdf_expand(prk, b"droidline v1 up", 32)
    k_dn = hkdf_expand(prk, b"droidline v1 down", 32)
    sas = '%06d' % (int.from_bytes(hkdf_expand(prk, b"droidline v1 sas", 4), 'big') % 1000000)
    pt = '{"event":"hello","device":"a1b2c3d4","boot":"7f3a9c","agent":"0.1.0"}'
    def env(key, seq, plaintext):
        nonce = b'\0\0\0\0' + struct.pack('>Q', seq)
        return {"seq":seq, "blob": b64u(AESGCM(key).encrypt(nonce, plaintext.encode(), b"droidline v1"))}
    return {"mode":mode, "line1":line1, "line2":line2, "th":th.hex(), "ikm":ikm.hex(), "k_up":k_up.hex(), "k_dn":k_dn.hex(), "sas":sas,
            "token": b64u(token) if with_token else None,
            "up_seq0": {"plaintext":pt, "envelope": env(k_up, 0, pt)},
            "dn_seq1": {"plaintext":'{"event":"welcome","server":"k3j9d0a2mq","ack":0}', "envelope": env(k_dn, 1, '{"event":"welcome","server":"k3j9d0a2mq","ack":0}')}}

relay_token = b"relay-test-token"
vec = {
  "comment": "Deterministic keys for tests only. Private keys are 32-byte big-endian scalars, public keys are uncompressed P-256 points, base64url without padding.",
  "keys": {
    "phone_static": {"d": dbytes(ps).hex(), "pub": b64u(pub(ps))},
    "server_static": {"d": dbytes(ss).hex(), "pub": b64u(pub(ss))},
    "phone_eph": {"d": dbytes(pe).hex(), "pub": b64u(pub(pe))},
    "server_eph": {"d": dbytes(se).hex(), "pub": b64u(pub(se))}
  },
  "handshakes": [case('auth', False), case('pair_code', False), case('pair_qr', True)],
  "relay": {
    "relay_token": relay_token.decode(),
    "device_ticket": {"server":"k3j9d0a2mq","device":"a1b2c3d4","ticket": b64u(hmac.new(relay_token, b"droidline device|k3j9d0a2mq|a1b2c3d4", hashlib.sha256).digest())},
    "enroll_ticket": {"server":"k3j9d0a2mq","expiry":1791360000,"ticket": b64u(hmac.new(relay_token, b"droidline enroll|k3j9d0a2mq|1791360000", hashlib.sha256).digest())}
  }
}
json.dump(vec, open('test-vectors.json','w',encoding='utf-8'), indent=2, ensure_ascii=False)
print("ok", vec["handshakes"][1]["sas"])
