import { render, screen, waitFor } from '@testing-library/react'

import WhatsAppPanel from './page'

function mockFetchOnceSessions(sessions: unknown[]) {
  const fetchMock = jest.fn(() =>
    Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve(sessions),
    } as Response),
  )
  global.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

describe('WhatsAppPanel', () => {
  const originalFetch = global.fetch

  afterEach(() => {
    global.fetch = originalFetch
    jest.restoreAllMocks()
  })

  it('renders 15 slots, all empty, when there are no sessions', async () => {
    mockFetchOnceSessions([])
    const { unmount } = render(<WhatsAppPanel />)

    const slots = await screen.findAllByTestId('wa-slot')
    expect(slots).toHaveLength(15)
    // Every empty slot exposes the connect affordance.
    expect(screen.getAllByText(/conectar novo número/i)).toHaveLength(15)
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
    // 1 occupied + 14 empty = 15 slots.
    expect(screen.getAllByTestId('wa-slot')).toHaveLength(15)
    expect(screen.getByText(/1 \/ 15 conectados/i)).toBeInTheDocument()

    unmount()
  })
})
