#!/usr/bin/python3

import git
import os.path
import re

ROOT = os.path.join(os.path.dirname(__file__), '..')

FIND_PR = re.compile(r'Merge pull request #(\d+) from').findall

ENTRY = """- %(title)s
  [#%(pr)s](https://github.com/strukturag/nextcloud-spreed-signaling/pull/%(pr)s)"""

SIMPLE_ENTRY = """- %(title)s"""

def main():
  repo = git.Repo(ROOT)
  latest = repo.tags[-1]
  print('Generating changelog since %s (commit %s)' % (latest, latest.commit))

  entries = []
  dependencies = []
  ignore = set()
  for commit in repo.iter_commits('%s..HEAD' % (latest.commit, )):
    if len(commit.parents) > 1:
      # Merge commit.
      ignore.add(commit.parents[-1])
      entries.append(commit)
    else:
      try:
        ignore.remove(commit)
      except KeyError:
        # Direct commit.
        entries.append(commit)
      else:
        # Commit is part of a merge.
        ignore.add(commit.parents[0])

  # Sort commits from old to new.
  for commit in reversed(entries):
    lines = [x.strip() for x in commit.message.strip().split('\n')]
    title = None
    for line in lines:
      if not line:
        title = ''
      elif title == '':
        title = line
        break

    if not title and len(lines) == 1:
      title = lines[0]
    assert line, (commit.message, )
    pr = FIND_PR(commit.summary)
    assert len(pr) <= 1, (commit.summary, )
    if len(pr) == 1:
      entry = ENTRY % {
        'title': title,
        'pr': pr[0],
      }
    elif len(pr) == 0:
      entry = SIMPLE_ENTRY % {
        'title': title,
      }

    if title.startswith('Bump '):
      dependencies.append(entry)
    else:
      print(entry)

  # Dependencies should be in a separate section at the end.
  if dependencies:
    print()
    print('### Dependencies')
    print('\n'.join(dependencies))

if __name__ == '__main__':
  main()
