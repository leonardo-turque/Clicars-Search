"""Status compacto do aquecimento — usado pelo monitor horário e manualmente.

Uso: python scripts/warmup_status.py [http://localhost:3001]
"""
import json
import sys
import urllib.request

base = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:3001"

try:
    with urllib.request.urlopen(base + "/api/v1/protect/numbers", timeout=15) as r:
        rows = json.load(r)
except Exception as e:
    print("STATUS-ERRO:", e)
    sys.exit(0)

for n in rows:
    phone = n.get("phone_number") or ""
    if not phone:
        continue  # perfil órfão de instância antiga deslogada
    flags = []
    if not n.get("connected"):
        flags.append("DESCONECTADO")
    if n.get("consecutive_errors", 0) > 0:
        flags.append("erros=%d" % n["consecutive_errors"])
    reason = (n.get("status_reason") or "").lower()
    if "circuito" in reason or "pausado" in reason or "resfriando" in reason:
        flags.append(n.get("status_reason"))
    tag = "  << " + "; ".join(flags) if flags else ""
    print(
        "%s | dia %s | enviados %s/%s (hora: %s, warmup: %s) | %s%s"
        % (
            phone,
            n.get("warmup_day"),
            n.get("sent_today"),
            n.get("daily_cap"),
            n.get("hourly_sent"),
            n.get("warmup_sent_today"),
            n.get("status_reason"),
            tag,
        )
    )
