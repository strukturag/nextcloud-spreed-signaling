#!/usr/bin/env python3
#
# Standalone signaling server for the Nextcloud Spreed app.
# Copyright (C) 2019 struktur AG
#
# @author Joachim Bauch <bauch@struktur.de>
#
# @license GNU AGPL version 3 or any later version
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <http://www.gnu.org/licenses/>.
#

try:
  # Fallback for Python2
  from cStringIO import StringIO
except ImportError:
  from io import StringIO
import json
import subprocess
import sys

URL = 'https://datahub.io/JohnSnowLabs/country-and-continent-codes-list/r/country-and-continent-codes-list-csv.json'

def tostr(s):
  if isinstance(s, bytes) and not isinstance(s, str):
    s = s.decode('utf-8')
  return s

try:
  unicode
except NameError:
  # Python 3 files are returning bytes by default.
  def opentextfile(filename, mode):
    if 'b' in mode:
      mode = mode.replace('b', '')
    return open(filename, mode, encoding='utf-8')
else:
  def opentextfile(filename, mode):
    return open(filename, mode)

def generate_map(filename):
  data = subprocess.check_output([
    'curl',
    '-L',
    URL,
  ])
  data = json.loads(tostr(data))
  continents = {}
  for entry in data:
    country = entry['Two_Letter_Country_Code']
    continent = entry['Continent_Code']
    continents.setdefault(country, []).append(continent)

  out = StringIO()
  out.write('package signaling\n')
  out.write('\n')
  out.write('// This file has been automatically generated, do not modify.\n')
  out.write('\n')
  out.write('var (\n')
  out.write('\tContinentMap map[string][]string = map[string][]string{\n')
  for country, continents in sorted(continents.items()):
    value = []
    for continent in continents:
      value.append('"%s"' % (continent))
    out.write('\t\t"%s": []string{%s},\n' % (country, ', '.join(value)))
  out.write('\t}\n')
  out.write(')\n')
  with opentextfile(filename, 'wb') as fp:
    fp.write(out.getvalue())

def main():
  if len(sys.argv) != 2:
    sys.stderr.write('USAGE: %s <filename>\n' % (sys.argv[0]))
    sys.exit(1)

  filename = sys.argv[1]
  generate_map(filename)

if __name__ == '__main__':
  main()
