'use client'

import { useState, useEffect, useCallback } from 'react'
import { API_URL, digitsOnly, formatPhone } from '@/lib/format'

type Status = 'CONNECTING' | 'CONNECTED' | 'DISCONNECTED'

interface WhatsAppSession {
  id: string
  jid?: string
  phone_number: string
  status: Status
  created_at: string
}

interface ConnectResponse {
  session_id: string
  qr_code: string // data:image/png;base64,...
}

interface ProtectSnap {
  session_id: string
  phone_number: string
  stage: 'WARMING' | 'MATURE' | 'PAUSED' | 'COOLING'
  daily_cap: number
  sent_today: number
  remaining_today: number
  health_score: number
  status_reason: string
}

const MAX_SLOTS = 15
const POLL_MS = 3000
const QR_TTL_SECONDS = 60

function Spinner({ className = 'h-4 w-4' }: { className?: string }) {
  return (
    <svg className={`animate-spin ${className}`} xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  )
}

function StatusBadge({ status }: { status: Status }) {
  const map = {
    CONNECTED: { dot: 'bg-green-500', text: 'text-green-700', bg: 'bg-green-50', label: 'Conectado', pulse: false },
    CONNECTING: { dot: 'bg-amber-500', text: 'text-amber-700', bg: 'bg-amber-50', label: 'Conectando…', pulse: true },
    DISCONNECTED: { dot: 'bg-gray-400', text: 'text-gray-600', bg: 'bg-gray-100', label: 'Desconectado', pulse: false },
  }[status]

  return (
    <span className={`inline-flex items-center gap-1.5 ${map.bg} ${map.text} rounded-full px-2.5 py-1 text-xs font-medium`}>
      <span className={`h-2 w-2 rounded-full ${map.dot} ${map.pulse ? 'animate-pulse' : ''}`} />
      {map.label}
    </span>
  )
}

function OccupiedSlot({
  session,
  health,
  onDelete,
  deleting,
}: {
  session: WhatsAppSession
  health?: ProtectSnap
  onDelete: (id: string) => void
  deleting: boolean
}) {
  const primary = digitsOnly(session.phone_number) === '554184376916'
  return (
    <div
      data-testid="wa-slot"
      className="surface p-5 flex flex-col gap-4"
    >
      <div className="flex items-start justify-between">
        <div className="h-10 w-10 rounded-xl bg-green-50 flex items-center justify-center">
          <svg className="h-5 w-5 text-green-600" fill="currentColor" viewBox="0 0 24 24">
            <path d="M.057 24l1.687-6.163a11.867 11.867 0 01-1.587-5.946C.16 5.335 5.495 0 12.05 0a11.817 11.817 0 018.413 3.488 11.824 11.824 0 013.48 8.414c-.003 6.557-5.338 11.892-11.893 11.892a11.9 11.9 0 01-5.688-1.448L.057 24zm6.597-3.807c1.676.995 3.276 1.591 5.392 1.592 5.448 0 9.886-4.434 9.889-9.885.002-5.462-4.415-9.89-9.881-9.892-5.452 0-9.887 4.434-9.889 9.884a9.86 9.86 0 001.51 5.26l-.999 3.648 3.477-.957zm11.387-5.464c-.074-.124-.272-.198-.57-.347-.297-.149-1.758-.868-2.031-.967-.272-.099-.47-.149-.669.149-.198.297-.768.967-.941 1.165-.173.198-.347.223-.644.074-.297-.149-1.255-.462-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.297-.347.446-.521.151-.172.2-.296.3-.495.099-.198.05-.372-.025-.521-.075-.148-.669-1.611-.916-2.206-.242-.579-.487-.501-.669-.51l-.57-.01c-.198 0-.52.074-.792.372s-1.04 1.016-1.04 2.479 1.065 2.876 1.213 3.074c.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.626.712.226 1.36.194 1.872.118.571-.085 1.758-.719 2.006-1.413.248-.695.248-1.29.173-1.414z" />
          </svg>
        </div>
        <StatusBadge status={session.status} />
      </div>

      <div className="min-w-0">
        <p className="text-base font-semibold text-gray-900 truncate">{formatPhone(session.phone_number)}</p>
        <p className="text-xs text-gray-400 truncate">
          Conectado em{' '}
          {new Date(session.created_at).toLocaleDateString('pt-BR', {
            day: '2-digit',
            month: '2-digit',
            year: 'numeric',
          })}
        </p>
        {primary && (
          <p className="mt-1 text-[11px] font-semibold text-amber-700">Número principal · em aquecimento (21 dias)</p>
        )}
        {health && (
          <p className="mt-1 text-xs text-gray-500">
            {health.sent_today}/{health.daily_cap} hoje · saúde {health.health_score}
          </p>
        )}
      </div>

      <button
        onClick={() => onDelete(session.id)}
        disabled={deleting}
        className="mt-auto w-full inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg border border-red-200 text-red-600 hover:bg-red-50 active:bg-red-100 disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
      >
        {deleting ? (
          <Spinner />
        ) : (
          <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        )}
        Desconectar número
      </button>
    </div>
  )
}

function EmptySlot({ onConnect }: { onConnect: () => void }) {
  return (
    <button
      data-testid="wa-slot"
      onClick={onConnect}
      className="group rounded-[22px] border-2 border-dashed border-blue-200 bg-blue-50/40 hover:border-blue-400 hover:bg-blue-50 transition-colors p-5 flex flex-col items-center justify-center gap-3 min-h-[184px]"
    >
      <div className="h-10 w-10 rounded-full bg-gray-100 group-hover:bg-blue-100 flex items-center justify-center transition-colors">
        <svg className="h-5 w-5 text-gray-400 group-hover:text-blue-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
        </svg>
      </div>
      <span className="text-sm font-semibold text-gray-600 group-hover:text-blue-700">Conectar novo número</span>
      <span className="max-w-[220px] text-center text-xs leading-relaxed text-gray-400">Leia o QR Code no aparelho que ficará responsável pelos atendimentos.</span>
    </button>
  )
}

function QRModal({
  qr,
  connecting,
  error,
  expired,
  secondsLeft,
  onRetry,
  onClose,
}: {
  qr: string | null
  connecting: boolean
  error: string | null
  expired: boolean
  secondsLeft: number
  onRetry: () => void
  onClose: () => void
}) {
  return (
    <div
      className="fixed inset-0 z-30 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      onClick={onClose}
    >
      <div
        className="modal-panel bg-white rounded-2xl shadow-xl w-full max-w-sm p-6 relative"
        onClick={(e) => e.stopPropagation()}
      >
        <button
          onClick={onClose}
          aria-label="Fechar"
          className="absolute top-4 right-4 text-gray-400 hover:text-gray-600"
        >
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>

        <h2 className="text-lg font-semibold text-gray-900 mb-1">Conectar WhatsApp</h2>
        <p className="text-sm text-gray-500 mb-5">
          Abra o WhatsApp no celular, vá em <strong>Aparelhos conectados</strong> e escaneie o código.
        </p>

        <div className="flex flex-col items-center justify-center min-h-[256px]">
          {error ? (
            <div className="text-center">
              <p className="text-sm text-red-600 mb-4">{error}</p>
              <button onClick={onRetry} className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium">
                Tentar novamente
              </button>
            </div>
          ) : connecting || !qr ? (
            <div className="flex flex-col items-center gap-3 text-gray-400">
              <Spinner className="h-8 w-8" />
              <span className="text-sm">Gerando QR Code…</span>
            </div>
          ) : expired ? (
            <div className="text-center">
              <div className="relative mb-4">
                <img src={qr} alt="QR Code expirado" className="h-56 w-56 rounded-lg opacity-20" />
                <span className="absolute inset-0 flex items-center justify-center text-sm font-medium text-gray-700">
                  QR Code expirado
                </span>
              </div>
              <button onClick={onRetry} className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium">
                Gerar novo QR Code
              </button>
            </div>
          ) : (
            <div className="flex flex-col items-center">
              <img src={qr} alt="QR Code para conectar o WhatsApp" className="h-56 w-56 rounded-lg border border-gray-100" />
              <p className="mt-4 text-xs text-gray-400">
                Expira em <span className="font-medium text-gray-600">{secondsLeft}s</span> · aguardando leitura…
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

export default function WhatsAppPanel() {
  const [sessions, setSessions] = useState<WhatsAppSession[]>([])
  const [health, setHealth] = useState<ProtectSnap[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  // Connect modal state
  const [modalOpen, setModalOpen] = useState(false)
  const [qr, setQr] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [connecting, setConnecting] = useState(false)
  const [connectError, setConnectError] = useState<string | null>(null)
  const [secondsLeft, setSecondsLeft] = useState(QR_TTL_SECONDS)
  const [expired, setExpired] = useState(false)

  const fetchSessions = useCallback(async () => {
    try {
      const res = await fetch(`${API_URL}/api/v1/whatsapp/sessions`)
      if (!res.ok) throw new Error(`Erro ${res.status}`)
      const data: WhatsAppSession[] = await res.json()
      setSessions(data ?? [])
      setError(null)
      const hRes = await fetch(`${API_URL}/api/v1/protect/numbers`)
      if (hRes.ok) {
        const hData: ProtectSnap[] = await hRes.json()
        setHealth(hData ?? [])
      }
    } catch (err) {
      if (err instanceof TypeError) {
        setError('Não foi possível conectar à API em ' + API_URL)
      } else {
        setError(err instanceof Error ? err.message : 'Erro inesperado.')
      }
    } finally {
      setLoading(false)
    }
  }, [])

  // Initial load + polling so statuses and new connections stay fresh.
  useEffect(() => {
    fetchSessions()
    const t = setInterval(fetchSessions, POLL_MS)
    return () => clearInterval(t)
  }, [fetchSessions])

  // When the pending number shows up as a session, pairing succeeded → close modal.
  useEffect(() => {
    if (!modalOpen || !pendingId) return
    if (sessions.some((s) => s.id === pendingId)) {
      setModalOpen(false)
      setQr(null)
      setPendingId(null)
    }
  }, [sessions, modalOpen, pendingId])

  // QR countdown.
  useEffect(() => {
    if (!qr || expired || connectError) return
    setSecondsLeft(QR_TTL_SECONDS)
    const t = setInterval(() => {
      setSecondsLeft((s) => {
        if (s <= 1) {
          clearInterval(t)
          setExpired(true)
          return 0
        }
        return s - 1
      })
    }, 1000)
    return () => clearInterval(t)
  }, [qr, expired, connectError])

  const startConnect = useCallback(async () => {
    setModalOpen(true)
    setConnecting(true)
    setConnectError(null)
    setExpired(false)
    setQr(null)
    setPendingId(null)
    try {
      const res = await fetch(`${API_URL}/api/v1/whatsapp/connect`)
      if (!res.ok) {
        const body = await res.json().catch(() => null)
        if (res.status === 409) throw new Error(body?.error || 'Limite de 15 números atingido.')
        throw new Error(body?.error || `Erro ${res.status}`)
      }
      const data: ConnectResponse = await res.json()
      setPendingId(data.session_id)
      setQr(data.qr_code)
    } catch (err) {
      setConnectError(err instanceof Error ? err.message : 'Falha ao gerar o QR Code.')
    } finally {
      setConnecting(false)
    }
  }, [])

  const closeModal = useCallback(() => {
    setModalOpen(false)
    setQr(null)
    setPendingId(null)
    setConnectError(null)
    setExpired(false)
  }, [])

  const handleDelete = useCallback(
    async (id: string) => {
      if (!window.confirm('Desconectar e remover este número?')) return
      setDeletingId(id)
      try {
        const res = await fetch(`${API_URL}/api/v1/whatsapp/sessions/${id}`, { method: 'DELETE' })
        if (!res.ok && res.status !== 404) {
          const body = await res.json().catch(() => null)
          throw new Error(body?.error || `Erro ${res.status}`)
        }
        await fetchSessions()
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Falha ao desconectar.')
      } finally {
        setDeletingId(null)
      }
    },
    [fetchSessions],
  )

  const connectedCount = sessions.filter((s) => s.status === 'CONNECTED').length
  const emptySlots = Math.max(0, MAX_SLOTS - sessions.length)

  return (
    <main className="min-h-screen">
      <div className="page-shell">
        {/* Header */}
        <div className="page-heading">
          <div>
            <p className="eyebrow">Canais conectados</p>
            <h1 className="page-title">Números de WhatsApp</h1>
            <p className="page-lead">Acompanhe as conexões usadas no atendimento e nas campanhas. O ritmo anti-ban fica na aba Proteção.</p>
          </div>
          <div className="inline-flex items-center gap-2 bg-white border border-gray-200 rounded-xl px-4 py-3 self-start">
            <span className="h-2 w-2 rounded-full bg-green-500" />
            <span className="text-sm font-medium text-gray-700">{`${connectedCount} / ${MAX_SLOTS} conectados`}</span>
          </div>
        </div>

        <div className="metric-grid">
          <div className="metric-card">
            <small>Conectados agora</small>
            <strong>{connectedCount}</strong>
          </div>
          <div className="metric-card">
            <small>Disponíveis</small>
            <strong>{emptySlots}</strong>
          </div>
          <div className="metric-card">
            <small>Operação</small>
            <strong className="!text-base text-green-700">Monitorada</strong>
          </div>
        </div>

        <div className="notice-safe mb-6 rounded-2xl px-4 py-4 text-sm leading-relaxed sm:px-5">
          <strong>Conexão não é autorização de marketing.</strong> Envie somente para pessoas que deram consentimento, identifique a empresa e respeite pedidos de saída. Limites técnicos não garantem que um número ficará livre de restrições.
        </div>

        {error && (
          <div className="flex items-start gap-3 bg-red-50 border border-red-200 text-red-700 rounded-xl px-5 py-4 mb-6">
            <svg className="h-5 w-5 mt-0.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
              <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clipRule="evenodd" />
            </svg>
            <p className="text-sm">{error}</p>
          </div>
        )}

        {loading ? (
          <div className="flex justify-center py-20 text-gray-400">
            <Spinner className="h-8 w-8" />
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {sessions.map((s) => (
              <OccupiedSlot
                key={s.id}
                session={s}
                health={health.find((h) => h.session_id === s.id || digitsOnly(h.phone_number) === digitsOnly(s.phone_number))}
                onDelete={handleDelete}
                deleting={deletingId === s.id}
              />
            ))}
            {emptySlots > 0 && <EmptySlot onConnect={startConnect} />}
          </div>
        )}
      </div>

      {modalOpen && (
        <QRModal
          qr={qr}
          connecting={connecting}
          error={connectError}
          expired={expired}
          secondsLeft={secondsLeft}
          onRetry={startConnect}
          onClose={closeModal}
        />
      )}
    </main>
  )
}
