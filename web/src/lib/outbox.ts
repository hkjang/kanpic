export type OutboxOperation = {
  id: string
  sheetId: string
  endpoint?: 'batch'|'paste'|'format'
  body: Record<string, unknown>
  attempts: number
  createdAt: number
}

const databaseName = 'kanpic-local'
const storeName = 'outbox'

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(storeName)) request.result.createObjectStore(storeName, { keyPath: 'id' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

async function transaction<T>(mode: IDBTransactionMode, callback: (store: IDBObjectStore, resolve: (value:T)=>void, reject:(reason?:unknown)=>void)=>void) {
  const db = await openDatabase()
  return new Promise<T>((resolve, reject) => {
    const tx = db.transaction(storeName, mode)
    callback(tx.objectStore(storeName), resolve, reject)
    tx.oncomplete = () => db.close()
    tx.onerror = () => reject(tx.error)
  })
}

export function enqueue(operation: OutboxOperation) {
  return transaction<void>('readwrite', (store, resolve, reject) => { const request=store.put(operation); request.onsuccess=()=>resolve(); request.onerror=()=>reject(request.error) })
}

export function remove(id: string) {
  return transaction<void>('readwrite', (store, resolve, reject) => { const request=store.delete(id); request.onsuccess=()=>resolve(); request.onerror=()=>reject(request.error) })
}

export function listOutbox() {
  return transaction<OutboxOperation[]>('readonly', (store, resolve, reject) => { const request=store.getAll(); request.onsuccess=()=>resolve(request.result.sort((a,b)=>a.createdAt-b.createdAt)); request.onerror=()=>reject(request.error) })
}

export async function flushOutbox(onApplied?: (operation:OutboxOperation, result:unknown)=>void) {
  if (!navigator.onLine) return 0
  const items = await listOutbox()
  let flushed = 0
  for (const item of items) {
    try {
      const path=item.endpoint==='format'?`/api/v1/sheets/${item.sheetId}/ranges:format`:`/api/v1/sheets/${item.sheetId}/cells:${item.endpoint==='paste'?'paste':'batch'}`
      const response = await fetch(path, { method:'PATCH', credentials:'same-origin', headers:{'Content-Type':'application/json'}, body:JSON.stringify(item.body) })
      if (!response.ok) {
        if (response.status >= 400 && response.status < 500 && response.status !== 409) await remove(item.id)
        break
      }
      const result = await response.json()
      await remove(item.id)
      flushed += 1
      onApplied?.(item, result)
    } catch { break }
  }
  return flushed
}
