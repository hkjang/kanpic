import { describe,expect,it } from 'vitest'
import { clipboardText,materializePaste,parseTabularText,shiftFormula,type KanpicClipboard } from './clipboard'

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

  it('rejects a destination outside the supported grid',()=>{
    expect(()=>materializePaste('1\n2',undefined,10_000,1)).toThrow('시트 한도')
  })
})
