import { describe,expect,it } from 'vitest'
import { clipboardText,materializeFill,materializePaste,parseTabularText,shiftFormula,type KanpicClipboard } from './clipboard'

describe('formula translation',()=>{
  it('moves relative references and keeps absolute axes',()=>{
    expect(shiftFormula('=A1+$B2+C$3+$D$4',2,3)).toBe('=D3+$B4+F$3+$D$4')
    expect(shiftFormula('=SUM(A1:B2)',1,1)).toBe('=SUM(B2:C3)')
  })

  it('does not translate references inside string literals',()=>{
    expect(shiftFormula('=IF(A1="A1","""B2""")',1,1)).toBe('=IF(B2="A1","""B2""")')
  })

  it('returns a reference error when a move crosses the sheet edge',()=>{
    expect(shiftFormula('=A1',-1,0)).toBe('=#REF!')
  })
})

describe('clipboard parsing',()=>{
  it('parses quoted tabs, line breaks and escaped quotes',()=>{
    expect(parseTabularText('"a\tb"\t"line 1\nline 2"\r\n"a""b"\t2')).toEqual([
      ['a\tb','line 1\nline 2'],['a"b','2'],
    ])
  })

  it('round trips the plain text representation',()=>{
    const payload:KanpicClipboard={version:1,sourceRow:1,sourceColumn:1,rows:2,columns:2,cells:[
      {rowOffset:0,columnOffset:0,value:'a\tb'},
      {rowOffset:0,columnOffset:1,formula:'=A1'},
      {rowOffset:1,columnOffset:0,value:'line 1\nline 2'},
      {rowOffset:1,columnOffset:1,value:2},
    ]}
    expect(parseTabularText(clipboardText(payload))).toEqual([['a\tb','=A1'],['line 1\nline 2','2']])
  })

  it('moves internal formulas relative to the copied source location',()=>{
    const payload:KanpicClipboard={version:1,sourceRow:1,sourceColumn:1,rows:1,columns:2,cells:[
      {rowOffset:0,columnOffset:0,value:2},
      {rowOffset:0,columnOffset:1,formula:'=A1*2'},
    ]}
    const pasted=materializePaste('',JSON.stringify(payload),4,4)
    expect(pasted).toEqual([
      {row:4,column:4,value:2,formula:undefined,style:undefined},
      {row:4,column:5,value:undefined,formula:'=D4*2',style:undefined},
    ])
    const offset:KanpicClipboard={version:1,sourceRow:5,sourceColumn:3,rows:1,columns:1,cells:[{rowOffset:0,columnOffset:0,formula:'=C5'}]}
    expect(materializePaste('',JSON.stringify(offset),10,7)[0].formula).toBe('=G10')
  })

  it('pastes formula results when values-only is requested',()=>{
    const payload:KanpicClipboard={version:1,sourceRow:1,sourceColumn:1,rows:1,columns:2,cells:[
      {rowOffset:0,columnOffset:0,value:2},
      {rowOffset:0,columnOffset:1,value:4,formula:'=A1*2'},
    ]}
    expect(materializePaste('',JSON.stringify(payload),3,4,true)).toEqual([
      {row:3,column:4,value:2,formula:undefined,style:undefined},
      {row:3,column:5,value:4,formula:undefined,style:undefined},
    ])
  })

  it('rejects a destination outside the supported grid',()=>{
    expect(()=>materializePaste('1\n2',undefined,10_000,1)).toThrow('시트 한도')
  })
})

describe('automatic fill',()=>{
  it('extends arithmetic numbers and preserves repeated styles',()=>{
    const payload:KanpicClipboard={version:1,sourceRow:1,sourceColumn:1,rows:2,columns:1,cells:[
      {rowOffset:0,columnOffset:0,value:1,style:{bold:true}},
      {rowOffset:1,columnOffset:0,value:3,style:{background:'#fef3c7'}},
    ]}
    expect(materializeFill(payload,{startRow:1,startColumn:1,endRow:5,endColumn:1})).toEqual([
      {row:3,column:1,value:5,formula:undefined,style:{bold:true}},
      {row:4,column:1,value:7,formula:undefined,style:{background:'#fef3c7'}},
      {row:5,column:1,value:9,formula:undefined,style:{bold:true}},
    ])
  })

  it('extends dates and trailing-number labels in either direction',()=>{
    const dates:KanpicClipboard={version:1,sourceRow:2,sourceColumn:1,rows:2,columns:1,cells:[
      {rowOffset:0,columnOffset:0,value:'2026-07-02'},
      {rowOffset:1,columnOffset:0,value:'2026-07-04'},
    ]}
    expect(materializeFill(dates,{startRow:1,startColumn:1,endRow:4,endColumn:1}).map(cell=>cell.value)).toEqual(['2026-06-30','2026-07-06'])
    const labels:KanpicClipboard={version:1,sourceRow:1,sourceColumn:2,rows:1,columns:1,cells:[{rowOffset:0,columnOffset:0,value:'항목01'}]}
    expect(materializeFill(labels,{startRow:1,startColumn:2,endRow:1,endColumn:5}).map(cell=>cell.value)).toEqual(['항목02','항목03','항목04'])
  })

  it('shifts formulas relative to each destination and excludes source cells',()=>{
    const payload:KanpicClipboard={version:1,sourceRow:1,sourceColumn:2,rows:1,columns:1,cells:[{rowOffset:0,columnOffset:0,value:10,formula:'=A1*10',style:{italic:true}}]}
    expect(materializeFill(payload,{startRow:1,startColumn:2,endRow:3,endColumn:2})).toEqual([
      {row:2,column:2,value:undefined,formula:'=A2*10',style:{italic:true}},
      {row:3,column:2,value:undefined,formula:'=A3*10',style:{italic:true}},
    ])
  })

  it('rejects targets outside the grid or operation limit',()=>{
    const payload:KanpicClipboard={version:1,sourceRow:1,sourceColumn:1,rows:1,columns:1,cells:[{rowOffset:0,columnOffset:0,value:1}]}
    expect(()=>materializeFill(payload,{startRow:1,startColumn:1,endRow:10_000,endColumn:2})).toThrow('최대 10,000셀')
    expect(()=>materializeFill(payload,{startRow:1,startColumn:1,endRow:10_001,endColumn:1})).toThrow('원본 선택 범위')
  })
})
