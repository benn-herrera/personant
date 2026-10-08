#
# !INSTALLED! from the adjagent repo — do not edit in place; edit the source repo and re-install.
# !BODY-SHA256! 09e1643db8ae4d1e0098a6f749cd77e323ab20d5c03ce50e691e95283434aaf5
#
"""Module entry point so ``python3 -m kb_tools.kb_driver <args>`` works."""

import sys

from .cli import main

if __name__ == "__main__":
    sys.exit(main())
