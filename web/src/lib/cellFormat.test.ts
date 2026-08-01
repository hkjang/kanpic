import {describe,expect,it} from 'vitest'
import {formatCellValue,wrapText} from './cellFormat'

describe('cell display formats',()=>{
  it('formats grouped numbers, percentages, currencies and scientific values',()=>{
    expect(formatCellValue(1234.5,{number_format:'#,##0.00'},'en-US')).toBe('1,234.50')
    expect(formatCellValue(.125,{number_format:'0.0%'},'en-US')).toBe('12.5%')
    expect(formatCellValue(2500,{number_format:'₩#,##0'},'en-US')).toBe('₩2,500')
    expect(formatCellValue(1234,{number_format:'0.00E+00'},'en-US')).toBe('1.23E+3')
    expect(formatCellValue(42,{number_format:'00000'},'en-US')).toBe('00042')
  })
  it('formats Excel serial dates and times without timezone drift',()=>{
    expect(formatCellValue(45292,{number_format:'yyyy-mm-dd'},'en-CA')).toBe('2024-01-01')
    expect(formatCellValue(.5,{number_format:'hh:mm'},'en-GB')).toBe('12:00')
    expect(formatCellValue(.75,{number_format:'h:mm AM/PM'},'en-US')).toBe('6:00 PM')
    expect(formatCellValue(1234,{number_format:'#,##0 ;[red](#,##0)'},'en-US')).toBe('1,234')
  })
  it('wraps words and long unbroken text',()=>{
    expect(wrapText('one two three',7,text=>text.length)).toEqual(['one two','three'])
    expect(wrapText('abcdefgh',3,text=>text.length)).toEqual(['abc','def','gh'])
  })
})
