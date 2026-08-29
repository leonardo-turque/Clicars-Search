import { render, screen, waitFor } from '@testing-library/react'

import WhatsAppPanel from './page'

function mockFetchOnceSessions(sessions: unknown[]) {
  const fetchMock = jest.fn((input: RequestInfo | URL) => {
    const url = String(input)
    const payload = url.includes('/protect/') ? [] : sessions
    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve(payload),
    } as Response)
  })
  global.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

describe('WhatsAppPanel', () => {
  const originalFetch = global.fetch

  afterEach(() => {
    global.fetch = originalFetch
    jest.restoreAllMocks()
  })

  it('renders a compact connection action and the available capacity when there are no sessions', async () => {
    mockFetchOnceSessions([])
    const { unmount } = render(<WhatsAppPanel />)

    const slots = await screen.findAllByTestId('wa-slot')
    expect(slots).toHaveLength(1)
    expect(screen.getByText(/conectar novo número/i)).toBeInTheDocument()
    expect(screen.getByText(/0 \/ 15 conectados/i)).toBeInTheDocument()

    unmount()
  })

  it('renders a connected number in an occupied slot with a green status', async () => {
    mockFetchOnceSessions([
      {
        id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
        phone_number: '5511999998888',
        status: 'CONNECTED',
        created_at: '2026-06-15T12:00:00Z',
      },
    ])
    const { unmount } = render(<WhatsAppPanel />)

    await waitFor(() => expect(screen.getByText(/99999-8888/)).toBeInTheDocument())
    expect(screen.getByText('Conectado')).toBeInTheDocument()
    // One occupied card plus one compact connection action.
    expect(screen.getAllByTestId('wa-slot')).toHaveLength(2)
    expect(screen.getByText(/1 \/ 15 conectados/i)).toBeInTheDocument()

    unmount()
  })
})
