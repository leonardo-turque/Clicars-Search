"""Monitor de aquecimento 24h — checa saúde, reconecta pausas e registra progresso.

Uso:
  python scripts/warmup_monitor_24h.py
  python scripts/warmup_monitor_24h.py --once   # uma checagem e sai
"""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

API = "http://localhost:3001"
PRIMARY = "554184376916"
PARTNER = "554185057452"
LOG = Path(__file__).resolve().parent / "warmup_monitor.log"


def api_get(path: str, timeout: int = 20):
    req = urllib.request.Request(API + path)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.load(r)


def api_post(path: str, body: dict | None = None, timeout: int = 20):
    data = json.dumps(body or {}).encode()
    req = urllib.request.Request(
        API + path,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as r:
        if r.status == 204:
            return None
        return json.loads(r.read().decode() or "null")


def log(msg: str) -> None:
    line = f"{datetime.now().isoformat(timespec='seconds')} | {msg}"
    print(line, flush=True)
    LOG.parent.mkdir(parents=True, exist_ok=True)
    with LOG.open("a", encoding="utf-8") as f:
        f.write(line + "\n")


def check_once() -> dict:
    actions: list[str] = []
    alerts: list[str] = []

    try:
        numbers = api_get("/api/v1/protect/numbers")
        sessions = api_get("/api/v1/whatsapp/sessions")
        contacts = api_get("/api/v1/protect/contacts")
    except urllib.error.URLError as e:
        alerts.append(f"API indisponível: {e}")
        return {"alerts": alerts, "actions": actions, "ok": False}

    session_by_phone = {
        s.get("phone_number"): s for s in sessions if s.get("phone_number")
    }
    target_phones = {PRIMARY, PARTNER}

    # Remove sessões órfãs desconectadas sem número
    for s in sessions:
        if not s.get("phone_number") and s.get("status") == "DISCONNECTED":
            sid = s.get("id")
            try:
                req = urllib.request.Request(
                    f"{API}/api/v1/whatsapp/sessions/{sid}",
                    method="DELETE",
                )
                urllib.request.urlopen(req, timeout=20)
                actions.append(f"removida sessão órfã {sid[:8]}…")
            except Exception as e:
                alerts.append(f"falha ao remover órfã {sid}: {e}")

    active_contacts = {c["phone"] for c in contacts if c.get("active")}
    for phone in target_phones:
        if phone not in active_contacts:
            label = "Chip principal Clicars" if phone == PRIMARY else "Chip Aquecimento (par)"
            try:
                api_post("/api/v1/protect/contacts", {"phone": phone, "label": label})
                actions.append(f"contato {phone} recadastrado")
            except Exception as e:
                alerts.append(f"falha ao cadastrar contato {phone}: {e}")

    summary = []
    for n in numbers:
        phone = n.get("phone_number") or ""
        if phone not in target_phones:
            continue
        connected = n.get("connected", False)
        stage = n.get("stage", "")
        reason = (n.get("status_reason") or "").lower()

        line = (
            f"{phone} | dia {n.get('warmup_day')}/21 | "
            f"warmup {n.get('warmup_sent_today')}/{n.get('warmup_budget_today')} | "
            f"total {n.get('sent_today')}/{n.get('daily_cap')} | "
            f"{'ON' if connected else 'OFF'} | {n.get('status_reason')}"
        )
        summary.append(line)

        if not connected:
            alerts.append(f"{phone} DESCONECTADO — reconecte via http://localhost:3000/whatsapp")
        if n.get("consecutive_errors", 0) >= 3:
            alerts.append(f"{phone} com {n['consecutive_errors']} erros seguidos")
        if "circuito" in reason or "pausado" in reason or "resfriando" in reason:
            sid = n.get("session_id")
            if sid and stage == "PAUSED":
                try:
                    api_post(f"/api/v1/protect/numbers/{sid}/resume")
                    actions.append(f"retomado {phone}")
                except Exception as e:
                    alerts.append(f"falha ao retomar {phone}: {e}")

        wa = session_by_phone.get(phone)
        if wa and wa.get("status") != "CONNECTED" and connected:
            alerts.append(f"{phone} WA status={wa.get('status')} (perfil diz conectado)")

    for phone in target_phones:
        if phone not in {n.get("phone_number") for n in numbers}:
            alerts.append(f"{phone} sem perfil de proteção")

    log("CHECK | " + " || ".join(summary) if summary else "CHECK | nenhum número alvo")
    for a in actions:
        log(f"ACTION | {a}")
    for a in alerts:
        log(f"ALERT | {a}")

    return {
        "ok": len(alerts) == 0,
        "summary": summary,
        "actions": actions,
        "alerts": alerts,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--once", action="store_true", help="executa uma vez e sai")
    args = parser.parse_args()

    started = datetime.now(timezone.utc)
    log(f"MONITOR START | alvo 24h desde {started.isoformat()} | primary={PRIMARY} partner={PARTNER}")

    result = check_once()
    if args.once:
        return 0 if result["ok"] else 1

  # modo contínuo: chamado pelo loop externo a cada hora
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
