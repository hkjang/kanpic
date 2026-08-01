import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { WorkbookSearchResult } from '../types'
import { WorkbookSearchDialog } from './WorkbookSearchDialog'

afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals()})

describe('WorkbookSearchDialog',()=>{
  it('searches the server and navigates to the selected cell',async()=>{
    const response:WorkbookSearchResult={workbook_id:'book-1',workbook_version:7,query:'매출',items:[{sheet_id:'sheet-2',sheet_name:'분기 실적',address:'C120',row:120,column:3,value:'매출 합계',formula:'=SUM(C2:C119)',matched_fields:['value']}],next_offset:50}
    const fetchMock=vi.fn().mockResolvedValue(new Response(JSON.stringify(response),{status:200,headers:{'Content-Type':'application/json'}}))
    vi.stubGlobal('fetch',fetchMock)
    const close=vi.fn(),navigate=vi.fn()
    render(<WorkbookSearchDialog open workbookId="book-1" version={7} onClose={close} onNavigate={navigate}/>)
    fireEvent.change(screen.getByRole('textbox',{name:'검색어'}),{target:{value:'매출'}})
    const option=await screen.findByRole('option',{name:/분기 실적/},{timeout:2000})
    expect(option).toHaveTextContent('C120')
    expect(screen.getByText('워크북 v7')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/workbooks/book-1/search?q=%EB%A7%A4%EC%B6%9C&limit=50',expect.objectContaining({signal:expect.any(AbortSignal)}))
    fireEvent.click(option)
    expect(navigate).toHaveBeenCalledWith(response.items[0])
    expect(close).toHaveBeenCalled()
  })

  it('opens a keyboard-selected result with Enter',async()=>{
    const response:WorkbookSearchResult={workbook_id:'book-1',workbook_version:8,query:'sum',items:[{sheet_id:'sheet-1',sheet_name:'Sheet1',address:'A1',row:1,column:1,value:3,formula:'=SUM(A2:A3)',matched_fields:['formula']}]}
    vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify(response),{status:200,headers:{'Content-Type':'application/json'}})))
    const navigate=vi.fn()
    render(<WorkbookSearchDialog open workbookId="book-1" version={8} onClose={()=>{}} onNavigate={navigate}/>)
    const input=screen.getByRole('textbox',{name:'검색어'})
    fireEvent.change(input,{target:{value:'sum'}})
    await screen.findByRole('option',{name:/Sheet1/},{timeout:2000})
    fireEvent.keyDown(input,{key:'Enter'})
    await waitFor(()=>expect(navigate).toHaveBeenCalledWith(response.items[0]))
  })
})
